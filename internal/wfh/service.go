package wfh

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/envutil"
	"github.com/inful/madhatter/internal/notify"
)

const (
	defaultMinOnsitePercentage = 50.0
	defaultMinOnsiteAbsolute   = 1
	defaultMaxDaysPerPeriod    = 2
	defaultPeriodDays          = 7
	// defaultSettlementDays covers one full period so a request
	// submitted on day N for any day in the current or next period
	// is settled by the next scheduler tick. The previous value of 2
	// left a five-day gap that surfaced as "still pending over the
	// weekend" reports.
	defaultSettlementDays     = 7
	defaultRequestHorizonDays = 90
	defaultPurgeEnabled       = true
	// defaultSettlementInterval is the period between scheduler
	// ticks. 15 minutes keeps the perceived lag between submitting a
	// request and seeing the approve/deny decision under 15 minutes
	// for ops that want a near-real-time feel, while still cheap on
	// CPU. A daily tick (the previous default) is fine for back-office
	// processing but means a request submitted at 4:55pm waits until
	// midnight to be settled.
	defaultSettlementInterval = 15 * time.Minute
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
	// SettlementInterval is the period between scheduler ticks. A
	// shorter value reduces the perceived latency between submission
	// and the approve/deny decision but adds light DB load. Defaults
	// to 15 minutes; can be set via WFH_SETTLEMENT_INTERVAL.
	SettlementInterval time.Duration
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
		SettlementInterval:  defaultSettlementIntervalFromEnv(),
		RequestHorizonDays:  envutil.Int("WFH_REQUEST_HORIZON_DAYS", defaultRequestHorizonDays),
		PurgeEnabled:        envutil.Bool("WFH_PURGE_ENABLED", defaultPurgeEnabled),
	}
}

// defaultSettlementIntervalFromEnv reads WFH_SETTLEMENT_INTERVAL
// (time.ParseDuration format like "15m", "1h", "30s") and falls back
// to defaultSettlementInterval on missing / unparseable values. The
// helper exists so the scheduler-test default and the production
// default can stay in lockstep without duplicating the parse logic.
func defaultSettlementIntervalFromEnv() time.Duration {
	return envutil.Duration("WFH_SETTLEMENT_INTERVAL", defaultSettlementInterval)
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
	// OverQuotaBy is the number of days the member is over
	// WFH_MAX_DAYS_PER_PERIOD. Zero in the normal case. Set when
	// an admin has marked the member as WFH past their quota
	// (the mark is a full override; the quota counter is still
	// incremented so the dashboard reflects the actual state).
	OverQuotaBy int
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

// IsEnabled reports whether the WFH feature is active. Gates the
// dashboard "WFH today" button, the same-day report handler, and any
// future caller that needs to surface a kill-switch.
func (s *Service) IsEnabled() bool {
	return s.cfg.Enabled
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
		slog.Info("WFH past-period purge completed", "deleted", deleted, "cutoff", cutoffStr)
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
	overQuotaBy := max(used-s.cfg.MaxDaysPerPeriod, 0)

	return QuotaStatus{
		PeriodStart: start.Format("2006-01-02"),
		PeriodEnd:   end.Format("2006-01-02"),
		Used:        used,
		Remaining:   remaining,
		OverQuotaBy: overQuotaBy,
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
		slog.Error("WFH recurring materializer failed", "error", err)
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
			slog.Error("WFH settlement error", "date", date, "error", err)
		}
	}
	return nil
}

// ReportToday is the same-day "unforeseen WFH" entry point. It is the
// fast-path version of the future-dated request flow: creates a
// pending row for today and settles it inline so the caller (web /
// API / CLI) can surface the outcome immediately without waiting for
// the scheduler tick.
//
// Policy: the request is created first, then run through the same
// settleDate logic the scheduler uses, so the capacity-floor
// enforcement stays uniform. If today is at the floor already the
// row is settled to "denied" — the dashboard reads it as On-site
// (no approved row), the user sees a flash banner. There is no
// "auto-approve anyway" escape hatch; fairness for the team wins
// over individual convenience.
//
// Returns the row with its final status. Errors map to:
//   - database.ErrWFHDisabled      — WFH_ENABLED=false
//   - database.ErrWFHOnHoliday      — today is a holiday
//   - database.ErrWFHDuplicateRequest — any row exists for (member, today)
//   - database.ErrWFHQuotaExhausted — member has used the full period quota
//
// The duplicate guard fires for any status (approved, pending,
// withdrawn, cancelled). A self-withdrawn recurring day is a
// resurrectable row that the user must revive through the regular
// request path, not by re-reporting.
func (s *Service) ReportToday(ctx context.Context, memberID string) (database.WFHRequest, error) {
	if !s.cfg.Enabled {
		return database.WFHRequest{}, database.ErrWFHDisabled
	}

	today, err := s.reportTodayValidate(ctx)
	if err != nil {
		return database.WFHRequest{}, err
	}

	exists, err := s.db.HasWFHRequestOnDate(ctx, memberID, today)
	if err != nil {
		return database.WFHRequest{}, err
	}
	if exists {
		return database.WFHRequest{}, database.ErrWFHDuplicateRequest
	}

	hasQuota, err := s.CheckQuota(ctx, memberID, today)
	if err != nil {
		return database.WFHRequest{}, err
	}
	if !hasQuota {
		return database.WFHRequest{}, database.ErrWFHQuotaExhausted
	}

	pending, err := s.db.CreateWFHRequest(ctx, memberID, today)
	if err != nil {
		return database.WFHRequest{}, err
	}

	// Settle inline for today. settleDate already swallows per-row
	// errors and continues, so a transient failure here leaves the row
	// in pending state; the next scheduler tick will pick it up.
	if sErr := s.settleDate(ctx, today, []database.WFHRequest{*pending}); sErr != nil {
		slog.Error("WFH: inline settle failed for ReportToday", "id", pending.ID, "error", sErr)
		return *pending, sErr
	}

	return s.reportTodayReRead(ctx, pending)
}

// MarkWFH is the admin override path: an admin records that a member
// worked from home today even though the member did not request it.
// The mark is a "correction" — the system said on-site, reality is WFH,
// the admin is recording reality. Two consequences of the override:
//
//   - The mark BYPASSES the per-member quota (WFH_MAX_DAYS_PER_PERIOD)
//     and the daily capacity floor (MinOnsiteCount). The admin is
//     taking responsibility for the action; the safety rails do not
//     block the override.
//
//   - The mark IS still counted in the quota and the floor once
//     recorded. The row is approved, every downstream query (quota
//     counter, on-site count, ICS feed) sees it. This keeps the
//     dashboard math honest — if the mark is excluded from the math,
//     the dashboard still shows the wrong state.
//
// The mark is today-only (matching the existing ReportToday and
// leave/report-sick patterns). The UNIQUE(member_id, date) constraint
// on wfh_requests guarantees idempotency: a second mark for the same
// (member, date) returns ErrWFHDuplicateRequest. The caller is
// expected to translate that into a "already marked" flash message.
func (s *Service) MarkWFH(ctx context.Context, memberID, date, actorUserID, actorName string) (database.WFHRequest, error) {
	if !s.cfg.Enabled {
		return database.WFHRequest{}, database.ErrWFHDisabled
	}
	dateTime, err := s.markWFHValidateDate(date)
	if err != nil {
		return database.WFHRequest{}, err
	}
	if s.db.IsHoliday(dateTime) {
		return database.WFHRequest{}, database.ErrWFHOnHoliday
	}
	if err := s.markWFHValidateMember(ctx, memberID); err != nil {
		return database.WFHRequest{}, err
	}

	id := uuid.NewString()
	if err := s.db.MarkAdminWFH(ctx, id, memberID, dateTime.Format("2006-01-02"), actorUserID); err != nil {
		return database.WFHRequest{}, err
	}

	req := s.markWFHBuildRequest(id, memberID, dateTime, actorUserID)
	s.markWFHNotify(ctx, req, actorName)
	return req, nil
}

// markWFHValidateDate enforces the today-only contract for the mark
// path. Pulled out of MarkWFH so the orchestrator function stays
// below the cyclop limit. Accepts an empty string as "use today"
// so the handler doesn't have to pre-fill the field.
func (s *Service) markWFHValidateDate(date string) (time.Time, error) {
	if date == "" {
		date = todayUTC().Format("2006-01-02")
	}
	if date != todayUTC().Format("2006-01-02") {
		return time.Time{}, database.ErrWFHInvalidDate
	}
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return time.Time{}, database.ErrWFHInvalidDate
	}
	return dateTime, nil
}

// markWFHValidateMember ensures the target member exists before we
// insert the row. A non-existent memberID is the most common admin
// form misconfiguration (a deleted user still in the dropdown), so
// we surface ErrWFHMemberNotFound distinctly from a generic DB
// error so the handler can render a clear flash.
func (s *Service) markWFHValidateMember(ctx context.Context, memberID string) error {
	if _, err := s.db.GetMemberByID(ctx, memberID); err != nil {
		if errors.Is(err, database.ErrWFHMemberNotFound) || errors.Is(err, sql.ErrNoRows) {
			return database.ErrWFHMemberNotFound
		}
		return err
	}
	return nil
}

// markWFHBuildRequest constructs the in-memory representation of
// the new row, including the audit fields. The marked_at timestamp
// is captured at this layer (not at insert time) so the value the
// caller sees matches the value written to disk.
func (s *Service) markWFHBuildRequest(id, memberID string, dateTime time.Time, actorUserID string) database.WFHRequest {
	req := database.WFHRequest{
		ID:            id,
		MemberID:      memberID,
		Date:          dateTime.Format("2006-01-02"),
		Status:        database.WFHStatusApproved,
		IsAdminMarked: true,
	}
	if actorUserID != "" {
		s := actorUserID
		req.MarkedBy = &s
	}
	now := time.Now().UTC()
	req.MarkedAt = &now
	return req
}

// markWFHNotify fires the WFHEvent so the member is told who marked
// them. nil notifier is a no-op (tests can omit the dependency).
func (s *Service) markWFHNotify(ctx context.Context, req database.WFHRequest, actorName string) {
	if s.notifier == nil {
		return
	}
	s.notifier.WFHStateChanged(ctx, notify.WFHEvent{
		RequestID:  req.ID,
		MemberID:   req.MemberID,
		MemberName: s.resolveMemberName(ctx, req.MemberID),
		Date:       req.Date,
		OldStatus:  "",
		NewStatus:  database.WFHStatusApproved,
		ActorName:  actorName,
	})
}

// reportTodayValidate returns today's date string after the feature-
// enabled and holiday gates pass. Pulled out of ReportToday so the
// orchestration function stays below the cyclop budget; the policy
// sequence (feature on → valid date → not a holiday) is short enough
// that one helper covers it.
func (s *Service) reportTodayValidate(_ context.Context) (string, error) {
	today := todayUTC().Format("2006-01-02")
	wfhDate, err := time.Parse("2006-01-02", today)
	if err != nil {
		return "", database.ErrWFHInvalidDate
	}
	if s.db.IsHoliday(wfhDate) {
		return "", database.ErrWFHOnHoliday
	}
	return today, nil
}

// reportTodayReRead fetches the row after settleDate commits its
// final status. A transient read failure here is non-fatal: the row
// was settled (the scheduler tick would have done the same thing),
// so we return the pre-read value and let the next caller pick up
// the post-settle state. Pulled out of ReportToday to keep its
// cyclomatic complexity below the limit.
func (s *Service) reportTodayReRead(ctx context.Context, pending *database.WFHRequest) (database.WFHRequest, error) {
	settled, err := s.db.GetWFHRequestByID(ctx, pending.ID)
	if err != nil {
		// The settle has already persisted; a fresh read by the
		// caller will surface the post-settle state. Returning the
		// pre-read row avoids a 500 for what is effectively a
		// caching miss.
		return *pending, nil //nolint:nilerr // deliberate: pre-read row is valid; caller will see post-settle state on next refresh
	}
	return *settled, nil
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

	// Approve up to availableSlots; deny the rest. Denials land with
	// a human-readable reason so the user is told why their request
	// was rejected (the row carries the reason to the WFH list page,
	// the admin manage page, and the email notification — no more
	// silent "Denied" tags).
	reason := denialReason(s.MinOnsiteCount(totalActive), slotsUsed)
	for i := range prioritized {
		status := database.WFHStatusApproved
		if i >= availableSlots {
			if err := s.db.DenyWFHRequest(ctx, prioritized[i].ID, reason); err != nil {
				slog.Error("WFH: failed to update denied request", "id", prioritized[i].ID, "error", err)
				continue
			}
			s.fireWFHStateChangedWithReason(ctx, prioritized[i], database.WFHStatusPending, database.WFHStatusDenied, reason, "system")
			continue
		}
		if err := s.db.UpdateWFHRequestStatus(ctx, prioritized[i].ID, status); err != nil {
			slog.Error("WFH: failed to update request", "id", prioritized[i].ID, "status", status, "error", err)
			continue
		}
		s.fireWFHStateChanged(ctx, prioritized[i], database.WFHStatusPending, status, "system")
	}
	return nil
}

// denialReason returns a human-readable explanation for a request
// being denied at settlement. The wording is generic enough to be
// useful regardless of the specific floor value, and the user can
// always re-read the help page for the floor math. The reason is
// stored in wfh_requests.denial_reason and shown on the WFH list
// page, the admin manage page, and the email notification.
func denialReason(minOnsite, slotsUsed int) string {
	// The settlement loop denies a request only when approving it
	// would drop on-site count below the floor. The wording is
	// the same regardless of which day or which floor, so the user
	// gets a clear, consistent message.
	return fmt.Sprintf(
		"On-site coverage would drop below the minimum (%d on-site required). %d members are already unavailable; approving more would leave the team under the floor.",
		minOnsite, slotsUsed,
	)
}

// fireWFHStateChanged invokes the wired notifier (if any) with the
// new WFH state. nil notifier is a no-op.
func (s *Service) fireWFHStateChanged(ctx context.Context, req database.WFHRequest, oldStatus, newStatus, actorName string) {
	s.fireWFHStateChangedInternal(ctx, req, oldStatus, newStatus, actorName, "")
}

// fireWFHStateChangedWithReason is the denial-path variant. The
// reason lands in the email template (and the on-disk
// wfh_requests.denial_reason column) so the user is told why
// their request was rejected instead of seeing a bare "Denied"
// status.
func (s *Service) fireWFHStateChangedWithReason(ctx context.Context, req database.WFHRequest, oldStatus, newStatus, reason, actorName string) {
	s.fireWFHStateChangedInternal(ctx, req, oldStatus, newStatus, actorName, reason)
}

// fireWFHStateChangedInternal invokes the wired notifier (if any)
// with the new WFH state. nil notifier is a no-op. The reason
// field is empty for non-denial transitions and populated for
// denials so the email template can surface it.
func (s *Service) fireWFHStateChangedInternal(ctx context.Context, req database.WFHRequest, oldStatus, newStatus, actorName, reason string) {
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
		Reason:     reason,
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
