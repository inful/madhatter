package calendar

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/inful/madhatter/internal/database"
)

// presenceMember is a member snapshot for template rendering. The fields
// exposed here are deliberately a small subset of the team-member row —
// enough to render a person's name and identifier without leaking
// sensitive internal state.
type presenceMember struct {
	ID    string
	Name  string
	Email string
}

// presenceSnapshot captures the per-day on-site/on-leave/WFH/HAT picture.
// It is built fresh per calendar request from the database, threaded
// through every event added on that day, and discarded when the day
// loop ends. There is no cross-request cache.
type presenceSnapshot struct {
	Date        string // "2006-01-02"
	IsWeekend   bool
	IsHoliday   bool
	HolidayName string

	TotalActive int
	OnSite      []presenceMember
	OnLeave     []presenceMember
	WFH         []presenceMember

	HATName     string
	HATIsCover  bool
	HATMemberID string

	// ShuffledOrder is the deterministic per-day random order of present
	// members. Driven by the snapshot's ShuffleSeed so operators can
	// change the salt independently of the meetings agenda shuffle.
	ShuffledOrder []presenceMember
}

// WFHMaterialiser is the narrow interface the calendar package uses to
// ensure recurring-WFH rows are present before reading them. The web
// layer wires a wfh.Service-backed implementation; tests can pass nil
// or a stub. Nil means the snapshot is computed against whatever is
// currently in the database (eventual consistency).
type WFHMaterialiser interface {
	EnsureRecurringMaterialized(ctx context.Context, start, end time.Time) (int, error)
}

// AssignWFHAssigner is the narrow interface the calendar package
// uses to invoke the seat-cap picker (Phase 2 of
// plans/assigned-wfh-plan.md) on a per-date basis. The web layer
// wires a wfh.Service-backed implementation; tests can pass nil.
// Nil is safe — RefreshFor skips the assignment hook entirely,
// matching the pre-Phase-2 behavior where the picker didn't
// exist.
type AssignWFHAssigner interface {
	AssignWFHForDate(ctx context.Context, date string) error
}

// HolidayLookup returns the holiday name for a date, or "" / false if
// the date is not a holiday. The web layer wires a holiday.Service
// adapter; tests can pass nil or a stub.
type HolidayLookup interface {
	GetHoliday(dateStr string) (name string, ok bool)
}

// noopHolidayLookup is the zero-value fallback used when no holiday
// service is wired in. Every date is non-holiday.
type noopHolidayLookup struct{}

func (noopHolidayLookup) GetHoliday(string) (string, bool) { return "", false }

// presenceBuilder builds a snapshot for a single date. It is reused
// across event adders on the same day inside the day loop.
type presenceBuilder struct {
	db              *database.DB
	wfhMaterialiser WFHMaterialiser
	wfhAssigner     AssignWFHAssigner
	holidayLookup   HolidayLookup
	shuffleSeed     string
}

func newPresenceBuilder(db *database.DB, m WFHMaterialiser, a AssignWFHAssigner, h HolidayLookup, seed string) *presenceBuilder {
	if h == nil {
		h = noopHolidayLookup{}
	}
	if seed == "" {
		seed = "support-rota-presence"
	}
	return &presenceBuilder{db: db, wfhMaterialiser: m, wfhAssigner: a, holidayLookup: h, shuffleSeed: seed}
}

// SnapshotFor computes the snapshot for dateStr without touching
// the database beyond reads. Idempotent and safe to call for
// past, today, and future dates. Use this when the caller
// wants a read-only view (e.g., a dashboard refresh that's
// not part of a settlement tick).
//
// The pre-Phase-2 behavior of `Build` is split:
//   - SnapshotFor: the read-only computation that was in Build.
//   - RefreshFor: SnapshotFor + the settlement hooks
//     (recurring materializer, picker).
//
// Calendar callers that want the "ensure recurring rows are
// visible + maybe assign" behavior should call RefreshFor so
// the on-demand assignment trigger fires. SnapshotFor is for
// callers that just want the current state.
func (b *presenceBuilder) SnapshotFor(ctx context.Context, dateStr string) (*presenceSnapshot, error) {
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil, fmt.Errorf("parse date %q: %w", dateStr, err)
	}

	active, leaveRecords, wfhRequests, err := b.loadActiveAndFlags(ctx, dateStr)
	if err != nil {
		return nil, err
	}

	onSite, onLeave, wfh := splitActiveByFlags(active, leaveRecords, wfhRequests)

	hatName, hatIsCover, err := getHATForDate(ctx, b.db, dateStr)
	if err != nil {
		return nil, fmt.Errorf("get HAT for date: %w", err)
	}

	holidayName, isHoliday := b.holidayLookup.GetHoliday(dateStr)
	shuffledMembers := b.shuffledMembersFor(active, dateStr)

	return b.assembleSnapshot(dateStr, date, active, onSite, onLeave, wfh, hatName, hatIsCover, holidayName, isHoliday, shuffledMembers), nil
}

// RefreshFor is the settlement hook surface. It composes
// SnapshotFor plus the settlement side-effects:
//
//  1. Materialize recurring rows for dateStr (idempotent).
//  2. Run AssignWFHForDate for dateStr — the seat-cap
//     picker (Phase 2). No-op for past dates, holidays,
//     and weekends (handled inside AssignWFHForDate).
//  3. Re-read the snapshot AFTER step 2 so the
//     just-assigned rows appear in the rendered events.
//     Idempotent in all sub-steps via UNIQUE constraints.
//
// Step 9 of plans/assigned-wfh-plan.md. Use RefreshFor when
// the caller's intent is "ensure fresh state for this date
// before rendering"; use SnapshotFor when the caller wants
// the current state without mutation.
func (b *presenceBuilder) RefreshFor(ctx context.Context, dateStr string) (*presenceSnapshot, error) {
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil, fmt.Errorf("parse date %q: %w", dateStr, err)
	}

	if mErr := b.materializeRecurring(ctx, date); mErr != nil {
		return nil, mErr
	}

	// Run the picker for today and future dates only. Past
	// dates are immutable per the past-date guard in
	// AssignWFHForDate. The picker is a no-op for past
	// dates anyway, but skipping the call here avoids the
	// slog.Warn path for every calendar render that walks
	// backwards.
	if b.wfhAssigner != nil && !date.Before(todayUTC()) {
		if aErr := b.wfhAssigner.AssignWFHForDate(ctx, dateStr); aErr != nil {
			// Don't fail the refresh; the picker failure is
			// already logged inside AssignWFHForDate's
			// error path. Returning the snapshot without
			// the just-assigned rows is the right
			// fallback — the calendar still renders,
			// just without the picker output.
			_ = aErr
		}
	}

	return b.SnapshotFor(ctx, dateStr)
}

// materializeRecurring makes sure recurring-WFH rows are present for the
// given date so the WFH query in Build sees them. nil-safe.
func (b *presenceBuilder) materializeRecurring(ctx context.Context, date time.Time) error {
	if b.wfhMaterialiser == nil {
		return nil
	}
	if _, mErr := b.wfhMaterialiser.EnsureRecurringMaterialized(ctx, date, date); mErr != nil {
		return fmt.Errorf("materialize recurring WFH: %w", mErr)
	}
	return nil
}

// loadActiveAndFlags loads the active-member list, the leave records
// for the date, and the approved WFH requests for the date. Returned
// in one call so the error returns don't add branches to Build.
func (b *presenceBuilder) loadActiveAndFlags(ctx context.Context, dateStr string) ([]database.TeamMember, []database.LeaveRecord, []database.WFHRequest, error) {
	active, err := b.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get active team members: %w", err)
	}
	leaveRecords, err := b.db.GetLeaveByDate(ctx, dateStr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get leave by date: %w", err)
	}
	wfhRequests, err := b.db.GetWFHRequestsByDateAndStatus(ctx, dateStr, database.WFHStatusApproved)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get WFH requests by date: %w", err)
	}
	return active, leaveRecords, wfhRequests, nil
}

// splitActiveByFlags partitions active members into on-site, on-leave,
// and WFH slices based on the leave and WFH flag records.
func splitActiveByFlags(active []database.TeamMember, leaveRecords []database.LeaveRecord, wfhRequests []database.WFHRequest) ([]presenceMember, []presenceMember, []presenceMember) {
	onLeaveSet := make(map[string]struct{}, len(leaveRecords))
	for i := range leaveRecords {
		onLeaveSet[leaveRecords[i].MemberID] = struct{}{}
	}
	wfhSet := make(map[string]struct{}, len(wfhRequests))
	for i := range wfhRequests {
		wfhSet[wfhRequests[i].MemberID] = struct{}{}
	}

	onSite := make([]presenceMember, 0, len(active))
	onLeave := make([]presenceMember, 0, len(leaveRecords))
	wfh := make([]presenceMember, 0, len(wfhRequests))
	for i := range active {
		m := active[i]
		pm := presenceMember{ID: m.ID, Name: m.Name, Email: m.Email}
		switch {
		case isInSet(onLeaveSet, m.ID):
			onLeave = append(onLeave, pm)
		case isInSet(wfhSet, m.ID):
			wfh = append(wfh, pm)
		default:
			onSite = append(onSite, pm)
		}
	}
	sortByName(onSite)
	sortByName(onLeave)
	sortByName(wfh)
	return onSite, onLeave, wfh
}

// shuffledMembersFor returns the stable random order of active members
// for the given date, in presenceMember form.
func (b *presenceBuilder) shuffledMembersFor(active []database.TeamMember, dateStr string) []presenceMember {
	shuffled := stableShuffle(active, b.shuffleSeed+"|"+dateStr+"|presence")
	out := make([]presenceMember, 0, len(shuffled))
	for i := range shuffled {
		out = append(out, presenceMember{ID: shuffled[i].ID, Name: shuffled[i].Name, Email: shuffled[i].Email})
	}
	return out
}

// assembleSnapshot builds the final presenceSnapshot from the
// pre-computed pieces. Pure function.
func (b *presenceBuilder) assembleSnapshot(
	dateStr string,
	date time.Time,
	active []database.TeamMember,
	onSite, onLeave, wfh []presenceMember,
	hatName string,
	hatIsCover bool,
	holidayName string,
	isHoliday bool,
	shuffledMembers []presenceMember,
) *presenceSnapshot {
	var hatMemberID string
	if hatName != "" {
		for i := range active {
			if active[i].Name == hatName {
				hatMemberID = active[i].ID
				break
			}
		}
	}
	weekday := date.Weekday()
	return &presenceSnapshot{
		Date:          dateStr,
		IsWeekend:     weekday == time.Saturday || weekday == time.Sunday,
		IsHoliday:     isHoliday,
		HolidayName:   holidayName,
		TotalActive:   len(active),
		OnSite:        onSite,
		OnLeave:       onLeave,
		WFH:           wfh,
		HATName:       hatName,
		HATIsCover:    hatIsCover,
		HATMemberID:   hatMemberID,
		ShuffledOrder: shuffledMembers,
	}
}

func isInSet(set map[string]struct{}, id string) bool {
	_, ok := set[id]
	return ok
}

// todayUTC returns midnight UTC of today. The RefreshFor past-date
// guard compares against this so the picker is skipped for any
// date before today's date boundary. Mirrors the helper in
// internal/wfh/service.go but kept local so the calendar package
// doesn't grow a wfh dependency just for one comparison.
func todayUTC() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func sortByName(members []presenceMember) {
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
}

func getHATForDate(ctx context.Context, db *database.DB, dateStr string) (string, bool, error) {
	assignments, err := db.GetAssignmentsByDate(ctx, dateStr)
	if err != nil {
		return "", false, err
	}
	for i := range assignments {
		if assignments[i].IsCover {
			return assignments[i].MemberName, true, nil
		}
	}
	for i := range assignments {
		if !assignments[i].IsCover {
			return assignments[i].MemberName, false, nil
		}
	}
	return "", false, nil
}

// stableShuffle returns a copy of members in a deterministic random
// order keyed by seedKey. Used for the snapshot's ShuffledOrder. Same
// algorithm as the meetings agenda shuffle, with a different seed.
func stableShuffle(members []database.TeamMember, seedKey string) []database.TeamMember {
	if len(members) <= 1 {
		return append([]database.TeamMember(nil), members...)
	}
	seed := stableSeed(seedKey)
	//nolint:gosec // Deterministic shuffle for template data; not used for security.
	rng := rand.New(rand.NewSource(int64(seed & math.MaxInt64)))
	out := append([]database.TeamMember(nil), members...)
	for i := len(out) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func stableSeed(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}
