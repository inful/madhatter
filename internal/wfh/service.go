package wfh

import (
	"context"
	"errors"
	"log"
	"math"
	"sort"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/envutil"
	"github.com/inful/madhatter/internal/notify"
)

const (
	defaultMinOnsitePercentage = 50.0
	defaultMinOnsiteAbsolute   = 1
	defaultMaxDaysPerPeriod    = 2
	defaultPeriodDays          = 7
	defaultSettlementDays      = 2
	defaultRequestHorizonDays  = 90
	defaultPurgeEnabled        = true
	// defaultPeriodAnchor is a known Monday used as the period epoch.
	defaultPeriodAnchor = "2026-01-05"
)

// Config holds the configuration for the WFH feature.
type Config struct {
	// Enabled controls whether the WFH feature is active.
	Enabled bool
	// MinOnsitePercentage is the minimum percentage of active members who must be on-site.
	MinOnsitePercentage float64
	// MinOnsiteAbsolute is the absolute minimum number of members who must be on-site.
	MinOnsiteAbsolute int
	// MaxDaysPerPeriod is the maximum number of WFH days (pending + approved) per quota period.
	MaxDaysPerPeriod int
	// PeriodDays is the length of one quota period in days.
	PeriodDays int
	// PeriodAnchor is a fixed past date (YYYY-MM-DD) used as the epoch for computing period boundaries.
	PeriodAnchor string
	// SettlementDays is how many days ahead pending requests are auto-settled.
	SettlementDays int
	// RequestHorizonDays is how many days ahead a WFH request can be submitted.
	RequestHorizonDays int
	// PurgeEnabled controls whether past-period wfh_requests rows are
	// automatically hard-deleted by the scheduler. When true (default),
	// the daily scheduler runs PurgePastPeriods after each settle tick.
	// Set to false to keep historical WFH rows indefinitely. Note that
	// the WFH feature itself (Enabled) gates the purge as well — when
	// Enabled is false the purge is skipped everywhere.
	PurgeEnabled bool
}

// LoadConfigFromEnv loads WFH configuration from environment variables.
func LoadConfigFromEnv() Config {
	return Config{
		Enabled:             envutil.Bool("WFH_ENABLED", true),
		MinOnsitePercentage: envutil.Float64("WFH_MIN_ONSITE_PERCENTAGE", defaultMinOnsitePercentage),
		MinOnsiteAbsolute:   envutil.Int("WFH_MIN_ONSITE_ABSOLUTE", defaultMinOnsiteAbsolute),
		MaxDaysPerPeriod:    envutil.Int("WFH_MAX_DAYS_PER_PERIOD", defaultMaxDaysPerPeriod),
		PeriodDays:          envutil.Int("WFH_PERIOD_DAYS", defaultPeriodDays),
		PeriodAnchor:        envutil.String("WFH_PERIOD_ANCHOR", defaultPeriodAnchor),
		SettlementDays:      envutil.Int("WFH_SETTLEMENT_DAYS", defaultSettlementDays),
		RequestHorizonDays:  envutil.Int("WFH_REQUEST_HORIZON_DAYS", defaultRequestHorizonDays),
		PurgeEnabled:        envutil.Bool("WFH_PURGE_ENABLED", defaultPurgeEnabled),
	}
}

// Service orchestrates WFH request settlement and quota management.
type Service struct {
	db       *database.DB
	cfg      Config
	notifier notify.Notifier
}

// NewService creates a new WFH service with the given database and configuration.
func NewService(db *database.DB, cfg Config) *Service {
	return &Service{db: db, cfg: cfg}
}

// SetNotifier wires a notifier that the service calls after each WFH
// state transition (settlement approve/deny, admin withdraw). nil
// disables notifications; tests can omit the dependency.
func (s *Service) SetNotifier(n notify.Notifier) {
	s.notifier = n
}

// Config returns the service configuration.
func (s *Service) Config() Config {
	return s.cfg
}

// QuotaStatus describes a member's WFH usage in the current period.
type QuotaStatus struct {
	PeriodStart string
	PeriodEnd   string
	Used        int
	Remaining   int
}

// ComputePeriodBounds computes the start and end of the quota period containing the given date.
func (s *Service) ComputePeriodBounds(date time.Time) (start, end time.Time, err error) {
	anchor, err := time.Parse("2006-01-02", s.cfg.PeriodAnchor)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid WFH_PERIOD_ANCHOR: " + s.cfg.PeriodAnchor)
	}

	const hoursPerDay = 24
	days := int(date.UTC().Truncate(hoursPerDay*time.Hour).Sub(anchor.UTC()).Hours() / hoursPerDay)
	periodIndex := days / s.cfg.PeriodDays
	if days < 0 {
		// Integer division rounds towards zero; adjust for negative offsets.
		periodIndex = (days - s.cfg.PeriodDays + 1) / s.cfg.PeriodDays
	}

	start = anchor.UTC().AddDate(0, 0, periodIndex*s.cfg.PeriodDays)
	end = start.AddDate(0, 0, s.cfg.PeriodDays-1)
	return start, end, nil
}

// IsPurgeEnabled reports whether the past-period purge is active. The
// purge is active only when both the WFH feature is enabled and the
// PurgeEnabled flag is true. Callers can use this to gate the CLI command
// and the admin web button without duplicating the policy.
func (s *Service) IsPurgeEnabled() bool {
	return s.cfg.Enabled && s.cfg.PurgeEnabled
}

// previousPeriodStart returns the start (inclusive) of the quota period
// immediately preceding the one containing refDate. The purge keeps rows
// from the current and previous periods, so this is the cut-off date:
// anything strictly before it is deleted.
func (s *Service) previousPeriodStart(refDate time.Time) (time.Time, error) {
	currentStart, _, err := s.ComputePeriodBounds(refDate)
	if err != nil {
		return time.Time{}, err
	}
	return currentStart.AddDate(0, 0, -s.cfg.PeriodDays), nil
}

// PurgePastPeriods hard-deletes every wfh_requests row whose date is
// strictly before the start of the previous quota period (relative to
// now). Returns the cutoff date (YYYY-MM-DD) and the number of rows
// deleted. When IsPurgeEnabled is false the function is a no-op and
// returns ("", 0, nil) — callers wanting an error path should check
// IsPurgeEnabled before calling.
//
// The deletion is non-recoverable; callers wanting a preview should
// call PurgePastPeriodsDryRun first. The scheduler runs this after each
// settle tick; the CLI and admin web button surface the dry-run by
// default to keep the destructive action explicit.
func (s *Service) PurgePastPeriods(ctx context.Context) (string, int64, error) {
	if !s.IsPurgeEnabled() {
		return "", 0, nil
	}
	cutoff, err := s.previousPeriodStart(time.Now().UTC())
	if err != nil {
		return "", 0, err
	}
	cutoffStr := cutoff.Format("2006-01-02")
	deleted, err := s.db.PurgeWFHRequestsBefore(ctx, cutoffStr)
	if err != nil {
		return cutoffStr, 0, err
	}
	if deleted > 0 {
		log.Printf("WFH past-period purge: deleted %d rows with date < %s\n", deleted, cutoffStr)
	}
	return cutoffStr, deleted, nil
}

// PurgePastPeriodsDryRun returns the cutoff date and the number of rows
// that PurgePastPeriods WOULD delete, without touching the table. Same
// gating as PurgePastPeriods: returns ("", 0, nil) when disabled.
func (s *Service) PurgePastPeriodsDryRun(ctx context.Context) (string, int64, error) {
	if !s.IsPurgeEnabled() {
		return "", 0, nil
	}
	cutoff, err := s.previousPeriodStart(time.Now().UTC())
	if err != nil {
		return "", 0, err
	}
	cutoffStr := cutoff.Format("2006-01-02")
	count, err := s.db.CountWFHRequestsBefore(ctx, cutoffStr)
	if err != nil {
		return cutoffStr, 0, err
	}
	return cutoffStr, count, nil
}

// GetQuotaStatus returns the quota status for the given member as of now.
func (s *Service) GetQuotaStatus(ctx context.Context, memberID string) (QuotaStatus, error) {
	start, end, err := s.ComputePeriodBounds(time.Now().UTC())
	if err != nil {
		return QuotaStatus{}, err
	}

	requests, err := s.db.GetWFHRequestsUsedInPeriod(ctx, memberID,
		start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return QuotaStatus{}, err
	}

	// Recurring-WFH occurrences are pre-materialized as approved rows by the
	// materializer, so they're already counted in len(requests). No separate
	// recurring-day accounting is needed at this layer.
	used := len(requests)
	remaining := max(s.cfg.MaxDaysPerPeriod-used, 0)

	return QuotaStatus{
		PeriodStart: start.Format("2006-01-02"),
		PeriodEnd:   end.Format("2006-01-02"),
		Used:        used,
		Remaining:   remaining,
	}, nil
}

// CanWithdraw reports whether the WFH request for the given date
// can still be withdrawn. A request is withdrawable as long as
// its date is today or later (i.e., the day has not yet passed).
// Once the date is in the past, the request is no longer
// withdrawable — the day has already been lived and the
// on-site roster has been acted on. This applies to both
// self-withdraw and admin-withdraw.
func (s *Service) CanWithdraw(wfhDate time.Time) bool {
	return !wfhDate.UTC().Before(todayUTC())
}

// MaxRequestDate returns the latest date (midnight UTC) up to which an
// ad-hoc WFH request can be submitted. Contractual recurring-WFH rows are
// produced by the materializer, which is not bounded by this horizon.
func (s *Service) MaxRequestDate() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, s.cfg.RequestHorizonDays)
}

// ValidateRequestDate reports whether the given date is within the request horizon.
// Returns nil if valid; ErrWFHDateTooFar if beyond the horizon; ErrWFHInvalidDate if unparseable.
func (s *Service) ValidateRequestDate(date string) error {
	wfhDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		return database.ErrWFHInvalidDate
	}
	if wfhDate.After(s.MaxRequestDate()) {
		return database.ErrWFHDateTooFar
	}
	return nil
}

// CheckQuota reports whether the member has remaining WFH days in the period containing date.
func (s *Service) CheckQuota(ctx context.Context, memberID, date string) (bool, error) {
	wfhDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		return false, database.ErrWFHInvalidDate
	}

	// Holidays are not valid WFH days. Mirror the DB-layer guard so forms
	// can disable the date picker and surface a clear error to the user.
	if s.db.IsHoliday(wfhDate) {
		return false, database.ErrWFHOnHoliday
	}

	start, end, err := s.ComputePeriodBounds(wfhDate)
	if err != nil {
		return false, err
	}

	requests, err := s.db.GetWFHRequestsUsedInPeriod(ctx, memberID,
		start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return false, err
	}

	// Recurring occurrences are already in len(requests) as pre-approved rows.
	// A request for a date that is the member's contractual recurring weekday
	// will fail with ErrWFHDuplicateRequest at the DB layer; we let that error
	// surface so the form can offer a withdraw-first flow.
	used := len(requests)
	return used < s.cfg.MaxDaysPerPeriod, nil
}

// SettlePendingRequests auto-approves or denies all pending WFH requests whose dates fall
// within the settlement window. It groups requests by date, enforces on-site minimums,
// and prioritizes by (fewest period days used ASC, earliest created_at ASC).
func (s *Service) SettlePendingRequests(ctx context.Context) error {
	cutoff := time.Now().UTC().AddDate(0, 0, s.cfg.SettlementDays)
	cutoffStr := cutoff.Format("2006-01-02")

	// Materialize recurring occurrences for the settlement window before
	// processing pending requests, so approved recurring rows are visible to
	// the on-site-minimums accounting. Materialization is idempotent and
	// bounded to the next SettlementDays.
	today := todayUTC()
	if _, err := s.EnsureRecurringMaterialized(ctx, today, cutoff); err != nil {
		log.Printf("WFH recurring materializer: %v\n", err)
	}

	pending, err := s.db.GetPendingForSettlement(ctx, cutoffStr)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	// Group by date.
	byDate := make(map[string][]database.WFHRequest)
	for i := range pending {
		byDate[pending[i].Date] = append(byDate[pending[i].Date], pending[i])
	}

	// Process each date in chronological order.
	dates := make([]string, 0, len(byDate))
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	for _, date := range dates {
		if err := s.settleDate(ctx, date, byDate[date]); err != nil {
			log.Printf("WFH settlement error for %s: %v\n", date, err)
		}
	}
	return nil
}

// settleDate settles all pending requests for a single date.
func (s *Service) settleDate(ctx context.Context, date string, pending []database.WFHRequest) error {
	// Count total active members.
	members, err := s.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return err
	}
	totalActive := len(members)
	if totalActive == 0 {
		return nil
	}

	onLeaveIDs, err := s.leaveMemberIDsForDate(ctx, date)
	if err != nil {
		return err
	}

	// Count already-approved WFH (from previously settled requests not in this batch).
	// Recurring occurrences are materialized as approved rows too, so they're
	// counted here and naturally subtract from the available on-site slots.
	approvedWFH, err := s.db.GetWFHRequestsByDateAndStatus(ctx, date, database.WFHStatusApproved)
	if err != nil {
		return err
	}

	// Compute the minimum on-site headcount.
	minOnsite := s.MinOnsiteCount(totalActive)

	// How many members are already unavailable for on-site duty?
	// Use unique member IDs to avoid double counting leave + WFH overlap.
	slotsUsed := countUniqueMembers(onLeaveIDs, memberIDsFromWFHRequests(approvedWFH))
	availableSlots := max(totalActive-slotsUsed-minOnsite, 0)

	// Sort pending requests by priority: fewest period-days used → earliest created_at.
	prioritized, err := s.prioritisePending(ctx, date, pending)
	if err != nil {
		return err
	}

	// Approve up to availableSlots; deny the rest.
	for i := range prioritized {
		status := database.WFHStatusDenied
		if i < availableSlots {
			status = database.WFHStatusApproved
		}
		if err := s.db.UpdateWFHRequestStatus(ctx, prioritized[i].ID, status); err != nil {
			log.Printf("WFH: failed to update request %s to %s: %v\n", prioritized[i].ID, status, err)
			continue
		}
		s.fireWFHStateChanged(ctx, prioritized[i], database.WFHStatusPending, status, "system")
	}
	return nil
}

// fireWFHStateChanged invokes the wired notifier (if any) with the
// new WFH state. nil notifier is a no-op.
func (s *Service) fireWFHStateChanged(ctx context.Context, req database.WFHRequest, oldStatus, newStatus, actorName string) {
	if s.notifier == nil {
		return
	}
	s.notifier.WFHStateChanged(ctx, notify.WFHEvent{
		RequestID:  req.ID,
		MemberID:   req.MemberID,
		MemberName: s.resolveMemberName(ctx, req.MemberID),
		Date:       req.Date,
		OldStatus:  oldStatus,
		NewStatus:  newStatus,
		ActorName:  actorName,
	})
}

// resolveMemberName looks up a member's display name for the
// notification. Returns "" if the member is not found; the notifier
// itself skips recipients with empty addresses but a missing name
// is non-fatal.
func (s *Service) resolveMemberName(ctx context.Context, memberID string) string {
	if memberID == "" {
		return ""
	}
	m, err := s.db.GetMemberByID(ctx, memberID)
	if err != nil || m == nil {
		return ""
	}
	return m.Name
}

// leaveMemberIDsForDate returns the set of team-member IDs on leave for date.
// Recurring-WFH availability is no longer tracked here — the materializer
// inserts those occurrences as approved wfh_requests rows, so they appear
// in the approved-WFH set that settleDate counts separately.
func (s *Service) leaveMemberIDsForDate(ctx context.Context, date string) (map[string]struct{}, error) {
	leaveRecords, err := s.db.GetLeaveByDate(ctx, date)
	if err != nil {
		return nil, err
	}

	ids := make(map[string]struct{}, len(leaveRecords))
	for i := range leaveRecords {
		ids[leaveRecords[i].MemberID] = struct{}{}
	}
	return ids, nil
}

func memberIDsFromWFHRequests(requests []database.WFHRequest) map[string]struct{} {
	ids := make(map[string]struct{}, len(requests))
	for i := range requests {
		ids[requests[i].MemberID] = struct{}{}
	}
	return ids
}

func countUniqueMembers(sets ...map[string]struct{}) int {
	unique := make(map[string]struct{})
	for _, set := range sets {
		for id := range set {
			unique[id] = struct{}{}
		}
	}
	return len(unique)
}

// MinOnsiteCount computes the minimum on-site count from config.
// It is the larger of MinOnsiteAbsolute and the percentage of
// active members rounded up — the same formula settleDate uses to
// decide available WFH slots. Exposed so the schedule matrix can
// flag days that have hit the WFH floor.
func (s *Service) MinOnsiteCount(totalActive int) int {
	const pctDivisor = 100.0
	pctMin := int(math.Ceil(float64(totalActive) * s.cfg.MinOnsitePercentage / pctDivisor))
	if pctMin < s.cfg.MinOnsiteAbsolute {
		return s.cfg.MinOnsiteAbsolute
	}
	return pctMin
}

type pendingWithUsage struct {
	req      database.WFHRequest
	usedDays int
}

// prioritisePending sorts pending requests by (usedDays ASC, createdAt ASC).
// usedDays comes from the period's existing wfh_requests count; recurring
// occurrences are already in that count as approved rows.
func (s *Service) prioritisePending(ctx context.Context, date string, pending []database.WFHRequest) ([]database.WFHRequest, error) {
	wfhDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, err
	}
	start, end, err := s.ComputePeriodBounds(wfhDate)
	if err != nil {
		return nil, err
	}
	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")

	items := make([]pendingWithUsage, 0, len(pending))
	for i := range pending {
		used, err := s.db.GetWFHRequestsUsedInPeriod(ctx, pending[i].MemberID, startStr, endStr)
		if err != nil {
			return nil, err
		}
		items = append(items, pendingWithUsage{req: pending[i], usedDays: len(used)})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].usedDays != items[j].usedDays {
			return items[i].usedDays < items[j].usedDays
		}
		return items[i].req.CreatedAt.Before(items[j].req.CreatedAt)
	})

	result := make([]database.WFHRequest, len(items))
	for i := range items {
		result[i] = items[i].req
	}
	return result, nil
}
