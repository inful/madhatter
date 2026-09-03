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

	// hoursPerDay is the calendar-hours-per-day constant used
	// to convert a duration into integer days. The co-presence
	// score is in calendar days per section 4 of
	// plans/assigned-wfh-plan.md.
	hoursPerDay = 24

	// coPresenceCohortCap is the maximum cohort size the SQL
	// query's IN list accepts. Larger cohorts are truncated;
	// the picker treats truncated cohort members as if they
	// weren't there (the candidate scores the sentinel).
	// Three matches the 3 explicit placeholders in the
	// GetLatestCoPresenceWithCohort query shape.
	coPresenceCohortCap = 3

	// backfillSafetyMultiplier bounds the backfill loop
	// iterations above the daysBack target. The weekday-skip
	// logic can skip weekends, so the loop needs more
	// iterations than daysBack to actually reach daysBack
	// working days. A multiplier of 2 gives ample slack.
	backfillSafetyMultiplier = 2
	// defaultCoPresenceEnabled is the kill switch for the
	// co-presence tiebreaker (step 10 of
	// plans/assigned-wfh-plan.md). Default true so the picker
	// rotates burden across the team by default; ops that don't
	// want the metric (privacy concerns, early-rollout testing)
	// can disable it without disabling the picker itself.
	defaultCoPresenceEnabled = true
	// defaultCoPresenceHorizonDays = 14 calendar days. 14
	// calendar days spans two work weeks for the picker to
	// consider "recent co-presence." The score itself is in
	// calendar days (see plans/assigned-wfh-plan.md section 4).
	defaultCoPresenceHorizonDays = 14
	// defaultCoPresenceRetentionDays = 30 calendar days.
	// Must be >= CoPresenceHorizonDays or the env loader
	// fails fast at boot. 30 keeps about a month of history,
	// which is enough for the picker to detect a "haven't been
	// on-site with the cohort recently" signal even when the
	// team takes a 2-week holiday mid-cycle.
	defaultCoPresenceRetentionDays = 30
	// defaultAssignmentEnabled is the kill switch for the
	// picker itself. Default true so the seat-cap math runs
	// automatically once WFH_SEAT_CAP is set; ops can disable
	// the picker without disabling the rest of the feature
	// (settlement, admin mark, withdrawal).
	defaultAssignmentEnabled = true
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
	// SeatCap is the maximum on-site headcount. When > 0, the
	// picker (step 7 of plans/assigned-wfh-plan.md) assigns WFH to
	// members whose voluntary + admin-marked + swap WFHs would
	// otherwise leave the on-site count over this number. When
	// 0 (the default), the picker is a no-op — the existing
	// behavior. Distinct from MinOnsiteAbsolute, which is the
	// *floor* (settlement denies WFH if approving would drop
	// on-site below that number). Same numerical sense,
	// opposite enforcement direction.
	SeatCap int
	// AssignmentEnabled is the kill switch for the picker. When
	// false, the picker is a no-op regardless of SeatCap.
	// Default true so the seat-cap math runs automatically
	// once SeatCap is set.
	AssignmentEnabled bool
	// CoPresenceEnabled is the kill switch for the
	// co-presence tiebreaker. When false, every candidate's
	// score is 0 and the picker degenerates to
	// (periodWFHCount, alphabetical). The picker itself
	// still runs — only the tiebreaker is suppressed.
	CoPresenceEnabled bool
	// CoPresenceHorizonDays is the calendar days back the
	// picker scans for prior co-presence. 14 (≈ two work
	// weeks) is the conservative default. Score itself is
	// in calendar days.
	CoPresenceHorizonDays int
	// CoPresenceRetentionDays is how many calendar days the
	// wfh_co_presence rows are kept. Must be >=
	// CoPresenceHorizonDays or the env loader fails fast
	// at boot. 30 keeps roughly a month of history.
	CoPresenceRetentionDays int
}

// LoadConfigFromEnv loads WFH configuration from environment variables.
// The five new knobs are added by the seat-cap picker (step 6 of
// plans/assigned-wfh-plan.md). The env var names mirror the
// doc-triggers table in the README and help.html — if you add or
// rename any of them, update those two files in the same commit
// per the project's documentation discipline.
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
		// Seat-cap picker (Phase 2). The five knobs are independent
		// of the existing floor / quota knobs; SeatCap can be 0
		// (the picker is a no-op) without disturbing MinOnsiteAbsolute
		// or MaxDaysPerPeriod.
		SeatCap:                 envutil.Int("WFH_SEAT_CAP", 0),
		AssignmentEnabled:       envutil.Bool("WFH_ASSIGNMENT_ENABLED", defaultAssignmentEnabled),
		CoPresenceEnabled:       envutil.Bool("WFH_COPRESENCE_ENABLED", defaultCoPresenceEnabled),
		CoPresenceHorizonDays:   envutil.Int("WFH_COPRESENCE_HORIZON_DAYS", defaultCoPresenceHorizonDays),
		CoPresenceRetentionDays: envutil.Int("WFH_COPRESENCE_RETENTION_DAYS", defaultCoPresenceRetentionDays),
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

// Validate checks the Config for invariant violations and returns
// an error suitable for logging + aborting process startup. The
// env loader calls this after LoadConfigFromEnv so misconfiguration
// fails fast rather than silently degrading to a half-working
// picker. The current invariants:
//
//   - CoPresenceRetentionDays >= CoPresenceHorizonDays.
//
// Without the invariant, the picker would scan rows that have
// already been pruned, returning a higher-than-expected score for
// every candidate. The horizon is the "what counts as recent"
// window and the retention is "how far back we keep history"; the
// retention must dominate the horizon so every horizon-day has
// data to read.
func (c Config) Validate() error {
	if c.CoPresenceRetentionDays < c.CoPresenceHorizonDays {
		return fmt.Errorf("wfh config: CoPresenceRetentionDays (%d) must be >= CoPresenceHorizonDays (%d) — the picker scans the horizon window and would see pruned rows otherwise",
			c.CoPresenceRetentionDays, c.CoPresenceHorizonDays)
	}
	return nil
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

// QuotaStatus describes a member's WFH usage in the quota period
// containing some reference date. The reference date is normally
// today (GetQuotaStatus) but can be a future date (GetQuotaStatusForDate)
// — useful for forms where the user picks a date in a different
// quota period than the one today belongs to.
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
	return s.GetQuotaStatusForDate(ctx, memberID, time.Now().UTC())
}

// GetQuotaStatusForDate returns the quota status for the quota period
// containing the given date. Mirrors GetQuotaStatus but lets the caller
// scope the period to a non-today date — needed by the WFH request
// form so the quota banner reflects the period the user is requesting
// for, not always the current period. A user with 0 remaining in the
// current period can still have 2 remaining in the next period, and
// the form should reflect that.
func (s *Service) GetQuotaStatusForDate(ctx context.Context, memberID string, refDate time.Time) (QuotaStatus, error) {
	start, end, err := s.ComputePeriodBounds(refDate)
	if err != nil {
		return QuotaStatus{}, err
	}

	requests, err := s.db.GetWFHRequestsVoluntaryInPeriod(ctx, memberID,
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

// IsHoliday reports whether the given date (YYYY-MM-DD) falls on a
// holiday according to the database's installed holiday checker.
// Returns false when the date fails to parse (the form layer will
// surface that as a separate error) or when no checker is installed
// (the production default installs one on database init).
func (s *Service) IsHoliday(date string) bool {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return false
	}
	return s.db.IsHoliday(t)
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

	requests, err := s.db.GetWFHRequestsVoluntaryInPeriod(ctx, memberID,
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
//
// Phase 2 of plans/assigned-wfh-plan.md: the picker (step 8) runs
// after settlement for every working day in the settlement window,
// independent of byDate. A date can be over cap even with zero
// pending WFH requests (everyone is on-site, no one asked to WFH,
// but the cap is exceeded) — running the picker over the full window
// catches that case. byDate iteration alone wouldn't.
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
		s.settleAssignmentPass(ctx, today, cutoff)
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
	s.settleAssignmentPass(ctx, today, cutoff)
	if err := s.AutoCancelExpiredSwaps(ctx); err != nil {
		slog.Error("WFH swap auto-cancel failed", "error", err)
	}
	if err := s.BackfillCoPresence(ctx); err != nil {
		slog.Error("WFH co-presence backfill failed", "error", err)
	}
	return nil
}

// settleAssignmentPass runs the seat-cap picker for every working
// day in the settlement window [today, cutoff]. Independent of
// byDate — a date can be over cap even with zero pending WFH
// requests. The picker is a no-op for past dates, holidays, and
// weekends (handled inside AssignWFHForDate). Errors are logged
// and the loop continues — one bad date must not block the rest
// of the window.
func (s *Service) settleAssignmentPass(ctx context.Context, today, cutoff time.Time) {
	if !s.cfg.Enabled || !s.cfg.AssignmentEnabled || s.cfg.SeatCap <= 0 {
		return
	}
	for d := today; !d.After(cutoff); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		date := d.Format("2006-01-02")
		if err := s.AssignWFHForDate(ctx, date); err != nil {
			slog.Error("WFH picker failed", "date", date, "error", err)
		}
	}
}

// WriteCoPresenceForPastDate records co-presence pairs for the
// given date. Step 11 of plans/assigned-wfh-plan.md. Called from
// RefreshFor (opportunistic — a calendar render that walks past
// dates catches the cohort while it's fresh) and from the
// scheduler's BackfillCoPresence pass (the authoritative
// eventually-consistent source).
//
// Implementation: read the on-site set for date (active -
// leave - permanent WFH - approved WFH), generate all C(n, 2)
// unordered pairs, write each via RecordWFHCoPresencePair
// (INSERT OR IGNORE — idempotent). The UNIQUE constraint
// guarantees correctness across concurrent writers; the
// CHECK constraint enforces canonical (a < b) ordering at
// the storage layer.
//
// Returns the number of pairs actually written (i.e., not
// skipped as duplicates). The pair count for an empty
// cohort is 0; for a single-member cohort, 0; for n>=2,
// C(n,2) = n*(n-1)/2.
func (s *Service) WriteCoPresenceForPastDate(ctx context.Context, dateStr string) (int, error) {
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return 0, database.ErrWFHInvalidDate
	}
	if !s.pastDateGuard(date) {
		return 0, nil
	}
	onSite, err := s.onSiteCohortIDs(ctx, dateStr)
	if err != nil {
		return 0, err
	}
	return s.writeCoPresencePairs(ctx, dateStr, onSite)
}

// pastDateGuard returns true when the given date is strictly
// before today (UTC midnight). Used by both the writer
// (WriteCoPresenceForPastDate) and the picker (AssignWFHForDate
// past-date branch) to short-circuit before any DB work.
func (s *Service) pastDateGuard(date time.Time) bool {
	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	return date.UTC().Before(today)
}

// onSiteCohortIDs returns the IDs of members who were on-site
// for date: active - leave - permanent WFH - approved WFH. Used
// by both the picker (to compute onSite and candidates) and
// the co-presence writer (to enumerate C(n, 2) pairs). Returns
// IDs in no particular order.
func (s *Service) onSiteCohortIDs(ctx context.Context, dateStr string) ([]string, error) {
	members, err := s.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, err
	}
	onLeaveIDs, err := s.leaveMemberIDsForDate(ctx, dateStr)
	if err != nil {
		return nil, err
	}
	approvedWFH, err := s.db.GetWFHRequestsByDateAndStatus(ctx, dateStr, database.WFHStatusApproved)
	if err != nil {
		return nil, err
	}
	permanentIDs := permanentWFHMemberIDs(members)
	approvedIDSet := memberIDsFromWFHRequests(approvedWFH)

	var onSite []string
	for i := range members {
		m := members[i]
		if _, onLeave := onLeaveIDs[m.ID]; onLeave {
			continue
		}
		if _, perm := permanentIDs[m.ID]; perm {
			continue
		}
		if _, wfh := approvedIDSet[m.ID]; wfh {
			continue
		}
		onSite = append(onSite, m.ID)
	}
	return onSite, nil
}

// minPairSize is the minimum cohort size for which co-presence
// pairs exist. n=0 or n=1 → no pairs; n>=2 → C(n,2) pairs.
const minPairSize = 2

// writeCoPresencePairs writes all C(n, 2) pairs for the given
// onSite set. Returns the count of newly-inserted pairs (zero
// when the cohort is too small, when every pair already
// exists, or when n < minPairSize).
func (s *Service) writeCoPresencePairs(ctx context.Context, dateStr string, onSite []string) (int, error) {
	if len(onSite) < minPairSize {
		return 0, nil
	}
	written := 0
	for i := range onSite {
		for j := i + 1; j < len(onSite); j++ {
			inserted, err := s.db.InsertWFHCoPresencePair(ctx, dateStr, onSite[i], onSite[j])
			if err != nil {
				return written, err
			}
			if inserted {
				written++
			}
		}
	}
	return written, nil
}

// AutoCancelExpiredSwaps flips every pending swap whose
// swap_date is strictly before today to status='cancelled'.
// Step 15 of plans/assigned-wfh-plan.md: SettlePendingRequests
// calls this after the existing settleDate / settleAssignment
// passes. The cutoff is today (UTC midnight). Past swaps are
// stale — the WFH day they referenced has already happened,
// so the assigned row's "you can swap this out" offer is no
// longer meaningful. The requester is left with their
// assigned WFH and the conflict guard releases, so a future
// "request a swap" submission can succeed without
// contending against the stale pending swap.
func (s *Service) AutoCancelExpiredSwaps(ctx context.Context) error {
	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	return s.db.CancelExpiredWFHSwaps(ctx, today)
}

// BackfillCoPresence runs WriteCoPresenceForPastDate for the
// last N working days, where N is WFH_COPRESENCE_RETENTION_DAYS
// bounded by 7 (the plan's recommendation; the full retention
// window would be 30 days by default but the daily tick only
// needs to catch the last week). Step 11 of
// plans/assigned-wfh-plan.md. Called from the scheduler
// (SettlePendingRequests) after each settlement tick.
//
// Eventual consistency: the daily backfill re-reads current
// state for each day and writes pair rows via INSERT OR
// IGNORE. Original (possibly incomplete) writes survive
// because INSERT OR IGNORE skips rows that already exist.
// Late-arriving leave approvals or WFH withdrawals on day
// N-2 are reflected in subsequent picker scores but not in
// the original co-presence rows — that's the
// eventually-consistent semantic the plan documents.
func (s *Service) BackfillCoPresence(ctx context.Context) error {
	if !s.cfg.CoPresenceEnabled {
		return nil
	}
	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	// 7 working days back, capped at retention. The upper bound
	// on `d` is set to 2 * daysBack as a generous safety margin
	// so the loop terminates even if the weekday-skip logic
	// skips too many days; daysBack decrements per actual write.
	daysBack := 7
	if s.cfg.CoPresenceRetentionDays < daysBack {
		daysBack = min(s.cfg.CoPresenceRetentionDays, daysBack)
	}
	maxIter := daysBack * backfillSafetyMultiplier
	for d, iter := today.AddDate(0, 0, -1), 0; iter < maxIter; d = d.AddDate(0, 0, -1) {
		iter++
		if iter >= maxIter {
			break
		}
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		date := d.Format("2006-01-02")
		if _, err := s.WriteCoPresenceForPastDate(ctx, date); err != nil {
			slog.Error("WFH co-presence backfill failed", "date", date, "error", err)
			continue
		}
		daysBack--
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

// AdminReassignWFH moves a system-assigned WFH row from one
// member to another in a single transaction. The cap is
// preserved: the original row flips to status='withdrawn'
// (the withdrawn_by column references users(id), so the
// actorUserID is stored raw — the reassign nature is
// surfaced in the notifier's ActorName suffix), and a new
// row with origin='assigned' lands for the replacement.
// Returns the new row's ID.
//
// Step 16 of plans/assigned-wfh-plan.md. The handler (web
// layer) calls this from POST /admin/wfh/{id}/reassign.
//
// Constraints:
//   - original row must be origin='assigned' and status='approved'.
//   - replacement member must be active, not exempt, not on
//     leave, not WFH on the date (mirrors the picker's
//     candidate filter).
//
// The transaction is best-effort: if the withdrawal succeeds
// but the new row insert fails (UNIQUE collision), the cap
// short-falls for one tick. The next SettlePendingRequests
// call re-issues the picker and re-balances. This is
// preferable to a strict transactional invariant because the
// admin-reassign path is rare and the recovery is automatic.
func (s *Service) AdminReassignWFH(ctx context.Context, originalRowID, replacementMemberID, actorUserID, actorName string) (string, error) {
	if !s.cfg.Enabled {
		return "", database.ErrWFHDisabled
	}
	original, err := s.db.GetWFHRequestByID(ctx, originalRowID)
	if err != nil {
		return "", err
	}
	if original.Origin != "assigned" {
		return "", database.ErrWFHAssigned
	}
	if original.Status != database.WFHStatusApproved {
		return "", database.ErrWFHNotApproved
	}

	ok, err := s.adminReassignTargetOK(ctx, original.Date, original.MemberID, replacementMemberID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("replacement member is not eligible (active, not on leave, not WFH on date, not exempt)")
	}

	if err := s.db.WithdrawAssignedWFHRequest(ctx, originalRowID, actorUserID); err != nil {
		return "", err
	}

	settledAt := time.Now().UTC()
	if err := s.db.CreateApprovedAssignedWFHRequest(ctx, replacementMemberID, original.Date, settledAt); err != nil {
		return "", err
	}

	s.adminReassignNotify(ctx, originalRowID, original, replacementMemberID, original.Date, actorName)
	return s.adminReassignResultID(ctx, replacementMemberID, original.Date)
}

// adminReassignTargetOK returns true when replacementMemberID
// is in the eligible-target set for date. The picker-style
// filter (active, not exempt, not on leave, not WFH on date,
// not the requester) is applied via EligibleSwapTargets.
// Extracted from AdminReassignWFH to keep the orchestrator
// under the cyclomatic-complexity budget.
func (s *Service) adminReassignTargetOK(ctx context.Context, date, requesterID, replacementMemberID string) (bool, error) {
	targets, err := s.EligibleSwapTargets(ctx, date, requesterID)
	if err != nil {
		return false, err
	}
	for i := range targets {
		if targets[i].ID == replacementMemberID {
			return true, nil
		}
	}
	return false, nil
}

// adminReassignNotify fires the WFHStateChanged events for
// both the original (withdrawn) and replacement (approved)
// rows. nil-safe — the notifier can be nil in tests.
func (s *Service) adminReassignNotify(ctx context.Context, originalRowID string, original *database.WFHRequest, replacementMemberID, date, actorName string) {
	if s.notifier == nil {
		return
	}
	s.notifier.WFHStateChanged(ctx, notify.WFHEvent{
		RequestID:  originalRowID,
		MemberID:   original.MemberID,
		MemberName: s.resolveMemberName(ctx, original.MemberID),
		Date:       date,
		OldStatus:  database.WFHStatusApproved,
		NewStatus:  database.WFHStatusWithdrawn,
		ActorName:  actorName,
	})
	replacementReq, _ := s.db.GetWFHRequestByMemberAndDate(ctx, replacementMemberID, date)
	if replacementReq != nil {
		s.notifier.WFHStateChanged(ctx, notify.WFHEvent{
			RequestID:  replacementReq.ID,
			MemberID:   replacementMemberID,
			MemberName: s.resolveMemberName(ctx, replacementMemberID),
			Date:       date,
			OldStatus:  "",
			NewStatus:  database.WFHStatusApproved,
			ActorName:  actorName,
		})
	}
}

// adminReassignResultID returns the just-inserted replacement
// row ID, or "" if the lookup failed. Best-effort.
func (s *Service) adminReassignResultID(ctx context.Context, replacementMemberID, date string) (string, error) {
	replacementReq, _ := s.db.GetWFHRequestByMemberAndDate(ctx, replacementMemberID, date)
	if replacementReq == nil {
		return "", nil
	}
	return replacementReq.ID, nil
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
// (member, date) returns ErrWFHDuplicateRequest. Returns the
// WFHRequest that was created. The caller (web handler) is expected
// to translate that into a "already marked" flash message.
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

// AssignWFHForDate runs the seat-cap picker for one date. If the
// on-site count for date would exceed cfg.SeatCap, members are
// picked (in order of fewest voluntary WFHs in the period, then
// alphabetical — the co-presence tiebreaker lands in step 10)
// and inserted as origin='assigned', status='approved' rows
// so the cap is met.
//
// Past-date guard: dates strictly before today are a no-op.
// Past attendance is immutable; the picker must not insert
// new WFH rows for days that have already been lived. The
// guard is the first statement of the function so any future
// caller — the scheduler (step 8), the presenceBuilder
// on-demand trigger (step 9), or a future admin CLI — is
// safe by default.
//
// Cap short-fall warning: when excess > len(candidates), the
// picker picks every candidate it can and emits a structured
// slog.Warn. The operator sees the gap in the scheduler log
// and can intervene manually (admin reassign, exempt toggles,
// or grow the team). The picker does not retry and does not
// surface per-member notifications for the gap — those would
// multiply the cost of a transient DB blip into something
// that affects users. The structured log is the v1 signal.
//
// Idempotency: two layers.
//  1. The candidate filter `NOT EXISTS (wfh_requests WHERE
//     member_id = id AND date = ?)` skips anyone who
//     already has any WFH row for the date (pending,
//     approved, recurring, assigned, or swap). So a re-run
//     for the same date sees zero candidates that already
//     have rows.
//  2. The UNIQUE(member_id, date) constraint backstops (1):
//     if two pickers race and both pick the same member, the
//     loser sees a duplicate-key error which
//     CreateApprovedAssignedWFHRequest translates to
//     ErrWFHDuplicateRequest — the picker treats that as
//     benign success.
//
// Re-run correctness: approvedWFHSet in the on-site math
// includes all origins, so a previously-assigned row
// correctly subtracts that member from onSite. On a re-run
// for the same date, approvedWFHSet already contains the
// assigned member, so excess reflects the post-assignment
// count. If the cap is already met, `IF onSite <= cap: RETURN`
// exits. If the cap is still exceeded (more members need to
// be assigned), only the additional members are picked —
// the originally-assigned ones are filtered out by NOT EXISTS.
//
// Step 7 of plans/assigned-wfh-plan.md.
func (s *Service) AssignWFHForDate(ctx context.Context, date string) error {
	if s.pickerDisabled() {
		return nil
	}
	wfhDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		return database.ErrWFHInvalidDate
	}
	if s.pastDateGuard(wfhDate) {
		return nil
	}
	if !s.isPickerActiveOnDate(wfhDate) {
		return nil
	}
	return s.runPicker(ctx, date)
}

// runPicker is the cap-arithmetic + selection + insert path,
// extracted from AssignWFHForDate to keep the orchestrator
// under the cyclomatic-complexity budget. The early-exit
// guards (picker-disabled, past-date, weekend/holiday) live
// in AssignWFHForDate; once those pass, this method does the
// actual work.
func (s *Service) runPicker(ctx context.Context, date string) error {
	members, err := s.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}
	onLeaveIDs, err := s.leaveMemberIDsForDate(ctx, date)
	if err != nil {
		return err
	}
	approvedWFH, err := s.db.GetWFHRequestsByDateAndStatus(ctx, date, database.WFHStatusApproved)
	if err != nil {
		return err
	}
	permanentWFHIDs := permanentWFHMemberIDs(members)
	approvedIDs := memberIDsFromWFHRequests(approvedWFH)
	onSite := len(members) - countUniqueMembers(onLeaveIDs, permanentWFHIDs, approvedIDs)

	capLimit := s.cfg.SeatCap
	if onSite <= capLimit {
		return nil
	}
	excess := onSite - capLimit

	candidates := s.assignCandidates(members, onLeaveIDs, approvedIDs)
	if excess > len(candidates) {
		s.logCapShortFall(date, excess, len(candidates))
		excess = len(candidates)
	}

	ordered, err := s.orderByPickerPriority(ctx, candidates, date)
	if err != nil {
		return err
	}
	return s.insertPicks(ctx, date, ordered[:excess])
}

// pickerDisabled returns true when the picker is feature-off,
// assignment-off, or has no seat cap configured. Centralized so
// the three early-exit guards can be combined into one boolean
// without bumping cyclop above the 10-branch budget.
func (s *Service) pickerDisabled() bool {
	return !s.cfg.Enabled || !s.cfg.AssignmentEnabled || s.cfg.SeatCap <= 0
}

// isPickerActiveOnDate returns false for holidays and weekends
// (the cap is meaningless when nobody is scheduled on-site)
// and true otherwise.
func (s *Service) isPickerActiveOnDate(date time.Time) bool {
	if s.db.IsHoliday(date) {
		return false
	}
	return date.Weekday() != time.Saturday && date.Weekday() != time.Sunday
}

// logCapShortFall emits the structured slog.Warn for a picker
// run that can't meet the cap. The short_by field names the gap
// so the operator can intervene manually (admin reassign,
// exempt toggles, or grow the team).
func (s *Service) logCapShortFall(date string, excess, candidateCount int) {
	if candidateCount == 0 {
		slog.Warn("WFH picker: cap short-fall, no candidates",
			"date", date,
			"excess", excess,
			"candidates", 0,
			"short_by", excess)
		return
	}
	slog.Warn("WFH picker: cap short-fall, candidates insufficient",
		"date", date,
		"excess", excess,
		"candidates", candidateCount,
		"short_by", excess-candidateCount)
}

// insertPicks inserts the chosen picks as origin='assigned',
// status='approved' wfh_requests rows and fires the WFHEvent
// for each so the assigned member is notified. Idempotent on
// UNIQUE collisions (concurrent picker races surface as
// ErrWFHDuplicateRequest which the picker treats as benign).
func (s *Service) insertPicks(ctx context.Context, date string, picks []database.TeamMember) error {
	settledAt := time.Now().UTC()
	for _, p := range picks {
		err := s.db.CreateApprovedAssignedWFHRequest(ctx, p.ID, date, settledAt)
		if err != nil {
			if errors.Is(err, database.ErrWFHDuplicateRequest) {
				// Race: a concurrent picker (or a manual admin
				// mark) already inserted this row. Benign — the
				// cap math reflects the row either way.
				continue
			}
			slog.Error("WFH picker: insert failed",
				"member_id", p.ID,
				"date", date,
				"error", err)
			continue
		}
		// Fire the notifier so the assigned member is told. Old
		// status is empty (no row existed); new status is
		// approved. Actor is "system" so the email can
		// distinguish picker-assigned rows from admin marks.
		req := database.WFHRequest{
			ID:       "assigned-" + date + "-" + p.ID,
			MemberID: p.ID,
			Date:     date,
			Status:   database.WFHStatusApproved,
			Origin:   "assigned",
		}
		s.fireWFHStateChanged(ctx, req, "", database.WFHStatusApproved, "system")
	}
	return nil
}

// assignCandidates returns the set of active members who can be
// assigned: not permanent WFH, not exempt from assignment, not on
// leave, not already WFH (any origin) for the date, and have no
// existing wfh_requests row for the date. The latter catches the
// edge case where a member has a pending request in
// settlement-pending state — the picker must not double-book them.
func (s *Service) assignCandidates(members []database.TeamMember, onLeaveIDs, approvedIDs map[string]struct{}) []database.TeamMember {
	out := make([]database.TeamMember, 0, len(members))
	for i := range members {
		m := &members[i]
		if !m.IsActive {
			continue
		}
		if m.IsPermanentWFH {
			continue
		}
		if m.IsExemptFromAssignment {
			continue
		}
		if _, onLeave := onLeaveIDs[m.ID]; onLeave {
			continue
		}
		if _, alreadyWFH := approvedIDs[m.ID]; alreadyWFH {
			continue
		}
		out = append(out, *m)
	}
	return out
}

// orderByPickerPriority sorts the candidate pool by
// (periodWFHCount ASC, score ASC, name ASC).
//
//   - periodWFHCount: voluntary WFHs in the current period.
//     Uses GetWFHRequestsVoluntaryInPeriod (filters
//     origin != 'assigned') so Assigned WFHs don't burn
//     quota — matches the user-facing promise.
//   - score: co-presence tiebreaker. Higher score = the
//     picker keeps the candidate on-site more. Lower
//     score = picked first. Scored in calendar days since
//     the candidate's last co-presence with the would-be
//     on-site cohort (the picker computes onSiteCohort
//     before sorting). NULL → horizon_days + 1
//     (history-clamp sentinel — see section 4 of
//     plans/assigned-wfh-plan.md, "Sentinel and
//     history-clamp").
//   - name: deterministic alphabetical tiebreaker.
//
// Co-presence is gated on cfg.CoPresenceEnabled. When false,
// every candidate scores horizon_days + 1 (the sentinel)
// and the picker degenerates to (periodWFHCount, name) —
// the same fallback the empty-cohort branch uses and that
// the first-week cold-start uses.
func (s *Service) orderByPickerPriority(ctx context.Context, candidates []database.TeamMember, date string) ([]database.TeamMember, error) {
	scored, err := s.scoreCandidates(ctx, candidates, date)
	if err != nil {
		return nil, err
	}
	sortScored(scored)
	out := make([]database.TeamMember, len(scored))
	for i := range scored {
		out[i] = scored[i].m
	}
	return out, nil
}

// pickerScored is the internal scoring type used by
// orderByPickerPriority. The picker keys off three fields:
// periodWFHCount (lowest wins), score (lowest wins), and the
// member's name (alphabetical tiebreaker).
type pickerScored struct {
	m              database.TeamMember
	periodWFHCount int
	score          int
}

// scoreCandidates computes periodWFHCount and the co-presence
// score for each candidate. Extracted from orderByPickerPriority
// to keep that function's cyclomatic complexity under the
// 10-branch budget.
func (s *Service) scoreCandidates(ctx context.Context, candidates []database.TeamMember, date string) ([]pickerScored, error) {
	nowUTC := time.Now().UTC()
	start, end, err := s.ComputePeriodBounds(nowUTC)
	if err != nil {
		return nil, err
	}
	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")

	cohortIDs, err := s.pickerCohortIDs(ctx, date, candidates)
	if err != nil {
		return nil, err
	}
	horizonStart := nowUTC.AddDate(0, 0, -s.cfg.CoPresenceHorizonDays)

	scored := make([]pickerScored, len(candidates))
	for i := range candidates {
		used, err := s.db.GetWFHRequestsVoluntaryInPeriod(ctx, candidates[i].ID, startStr, endStr)
		if err != nil {
			return nil, err
		}
		score, err := s.coPresenceScore(ctx, candidates[i].ID, cohortIDs, horizonStart, nowUTC)
		if err != nil {
			return nil, err
		}
		scored[i] = pickerScored{m: candidates[i], periodWFHCount: len(used), score: score}
	}
	return scored, nil
}

// coPresenceScore returns the calendar-days-since-last-co-
// presence score for candidateID against cohortIDs, with the
// history-clamp applied. Returns horizon+1 when:
//   - co-presence is disabled (kill switch)
//   - the cohort is empty
//   - the candidate has no history within the horizon window
//   - the most-recent co-presence is older than horizon_days
func (s *Service) coPresenceScore(ctx context.Context, candidateID string, cohortIDs []string, horizonStart, nowUTC time.Time) (int, error) {
	sentinel := s.cfg.CoPresenceHorizonDays + 1
	if !s.cfg.CoPresenceEnabled || len(cohortIDs) == 0 {
		return sentinel, nil
	}
	last, err := s.db.GetLatestCoPresenceWithCohort(ctx, candidateID, cohortIDs, horizonStart, nowUTC)
	if err != nil {
		return sentinel, err
	}
	if last.IsZero() {
		return sentinel, nil
	}
	raw := int(nowUTC.Sub(last).Hours() / hoursPerDay)
	if raw < sentinel {
		return raw, nil
	}
	return sentinel, nil
}

// sortScored orders by (periodWFHCount ASC, score ASC, name ASC).
// The alphabetical tiebreaker keeps the picker deterministic
// across re-runs and across the scheduler / on-demand trigger
// paths.
func sortScored(scored []pickerScored) {
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].periodWFHCount != scored[j].periodWFHCount {
			return scored[i].periodWFHCount < scored[j].periodWFHCount
		}
		if scored[i].score != scored[j].score {
			return scored[i].score < scored[j].score
		}
		return scored[i].m.Name < scored[j].m.Name
	})
}

// pickerCohortIDs returns the cohort for the co-presence score.
// Per section 4 of plans/assigned-wfh-plan.md the cohort is
// "members getting on-site today" — every member who COULD
// be on-site. That includes the candidates themselves (the
// picker hasn't decided yet whether to assign them WFH), and
// excludes only the definite non-on-site members: on-leave,
// permanent WFH, and approved WFH today.
//
// On weekends / holidays the cohort is empty (every candidate
// scores the sentinel). On dates where everyone is exempt /
// on leave, the cohort is also empty. The picker degenerates
// to (periodWFHCount, alphabetical) in both cases — same
// fallback the first-week cold-start uses.
//
// The return is capped at 3 IDs because the SQL query has 3
// placeholders in the IN list (sqlc v1.28 doesn't support
// dynamic slice expansion in this query shape). Larger cohorts
// are truncated — the picker still picks the lowest-scoring
// 3 cohort members, but candidates with co-presence against
// a non-truncated cohort member still get the sentinel. This
// matches the "empty cohort" fallback the plan documents.
func (s *Service) pickerCohortIDs(ctx context.Context, date string, _ []database.TeamMember) ([]string, error) {
	members, err := s.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, err
	}
	onLeaveIDs, err := s.leaveMemberIDsForDate(ctx, date)
	if err != nil {
		return nil, err
	}
	approvedWFH, err := s.db.GetWFHRequestsByDateAndStatus(ctx, date, database.WFHStatusApproved)
	if err != nil {
		return nil, err
	}
	permanentIDs := permanentWFHMemberIDs(members)
	approvedIDSet := memberIDsFromWFHRequests(approvedWFH)

	var cohort []string
	for i := range members {
		m := members[i]
		if _, onLeave := onLeaveIDs[m.ID]; onLeave {
			continue
		}
		if _, perm := permanentIDs[m.ID]; perm {
			continue
		}
		if _, wfh := approvedIDSet[m.ID]; wfh {
			continue
		}
		cohort = append(cohort, m.ID)
		if len(cohort) == coPresenceCohortCap {
			break
		}
	}
	return cohort, nil
}

// permanentWFHMemberIDs extracts the set of permanent-WFH member
// IDs from a slice of TeamMember. Permanent-WFH members never come
// on-site, so they're subtracted from the on-site count but also
// excluded from the picker candidate pool (assignCandidates).
func permanentWFHMemberIDs(members []database.TeamMember) map[string]struct{} {
	ids := make(map[string]struct{}, len(members))
	for i := range members {
		if members[i].IsPermanentWFH {
			ids[members[i].ID] = struct{}{}
		}
	}
	return ids
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

// ResolveMemberName is the public wrapper for resolveMemberName,
// exposed so the swap inbox (web layer) can attach the
// requester's display name to each pending swap without
// duplicating the GetMemberByID lookup. Used in step 14 of
// plans/assigned-wfh-plan.md.
func (s *Service) ResolveMemberName(ctx context.Context, memberID string) string {
	return s.resolveMemberName(ctx, memberID)
}

// EligibleSwapTargets returns the active members who could
// swap onto this date: not the requester, not on leave, not
// WFH today, not exempt. Mirrors the picker's candidate
// filter (Phase 2 / step 7) so the dropdown contains the
// same set the picker would have picked from.
//
// Used by the swap form (step 14) to populate the
// target_member_id select. Exposed as a public service
// method so the web layer doesn't have to re-implement the
// picker's eligibility rules.
func (s *Service) EligibleSwapTargets(ctx context.Context, dateStr, requesterID string) ([]database.TeamMember, error) {
	members, err := s.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, err
	}
	onLeaveIDs, err := s.leaveMemberIDsForDate(ctx, dateStr)
	if err != nil {
		return nil, err
	}
	approvedWFH, err := s.db.GetWFHRequestsByDateAndStatus(ctx, dateStr, database.WFHStatusApproved)
	if err != nil {
		return nil, err
	}
	approvedIDs := memberIDsFromWFHRequests(approvedWFH)

	out := make([]database.TeamMember, 0, len(members))
	for i := range members {
		m := members[i]
		if !m.IsActive {
			continue
		}
		if m.ID == requesterID {
			continue
		}
		if m.IsExemptFromAssignment {
			continue
		}
		if _, onLeave := onLeaveIDs[m.ID]; onLeave {
			continue
		}
		if _, wfh := approvedIDs[m.ID]; wfh {
			continue
		}
		out = append(out, m)
	}
	return out, nil
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
		used, err := s.db.GetWFHRequestsVoluntaryInPeriod(ctx, pending[i].MemberID, startStr, endStr)
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
