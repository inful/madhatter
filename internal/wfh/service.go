package wfh

import (
	"context"
	"errors"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/inful/madhatter/internal/database"
)

const (
	defaultMinOnsitePercentage = 50.0
	defaultMinOnsiteAbsolute   = 1
	defaultMaxDaysPerPeriod    = 2
	defaultPeriodDays          = 7
	defaultSettlementDays      = 2
	defaultWithdrawalHours     = 24
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
	// WithdrawalHours is how many hours before a WFH day an admin can still withdraw it.
	WithdrawalHours int
}

// LoadConfigFromEnv loads WFH configuration from environment variables.
func LoadConfigFromEnv() Config {
	cfg := Config{
		Enabled:             parseBoolEnv("WFH_ENABLED", true),
		MinOnsitePercentage: parseFloat64Env("WFH_MIN_ONSITE_PERCENTAGE", defaultMinOnsitePercentage),
		MinOnsiteAbsolute:   parseIntEnv("WFH_MIN_ONSITE_ABSOLUTE", defaultMinOnsiteAbsolute),
		MaxDaysPerPeriod:    parseIntEnv("WFH_MAX_DAYS_PER_PERIOD", defaultMaxDaysPerPeriod),
		PeriodDays:          parseIntEnv("WFH_PERIOD_DAYS", defaultPeriodDays),
		PeriodAnchor:        parseStringEnv("WFH_PERIOD_ANCHOR", defaultPeriodAnchor),
		SettlementDays:      parseIntEnv("WFH_SETTLEMENT_DAYS", defaultSettlementDays),
		WithdrawalHours:     parseIntEnv("WFH_WITHDRAWAL_HOURS", defaultWithdrawalHours),
	}
	return cfg
}

// Service orchestrates WFH request settlement and quota management.
type Service struct {
	db  *database.DB
	cfg Config
}

// NewService creates a new WFH service with the given database and configuration.
func NewService(db *database.DB, cfg Config) *Service {
	return &Service{db: db, cfg: cfg}
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

// CanWithdraw reports whether an admin may still withdraw the WFH request for the given date.
func (s *Service) CanWithdraw(wfhDate time.Time) bool {
	deadline := wfhDate.UTC().Add(-time.Duration(s.cfg.WithdrawalHours) * time.Hour)
	return time.Now().UTC().Before(deadline)
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
	minOnsite := s.minOnsiteCount(totalActive)

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
		}
	}
	return nil
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

// minOnsiteCount computes the minimum on-site count from config.
func (s *Service) minOnsiteCount(totalActive int) int {
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

// --- env helpers ---

func parseBoolEnv(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func parseIntEnv(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

func parseFloat64Env(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func parseStringEnv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}
