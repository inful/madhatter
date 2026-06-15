package rota

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inful/madhatter/internal/database"
)

// HolidayChecker is a function that checks if a date should be skipped due to holidays.
type HolidayChecker func(date time.Time) bool

// hoursPerDay is used to truncate a time.Time to midnight UTC before
// comparing it to the stored state date. The state stores a DATE (no
// time component), so any incoming time.Time must be normalized before
// the equality check.
const hoursPerDay = 24

type Engine struct {
	db             *database.DB
	holidayChecker HolidayChecker
	notifier       CoverNotifier
}

// CoverNotifier is the subset of the notify.Notifier interface that
// the engine needs. The engine fires CoverAssigned after a leave has
// been processed and covers assigned. nil disables notifications;
// tests can omit the dependency.
type CoverNotifier interface {
	CoverAssigned(ctx context.Context, e CoverEvent)
}

// CoverEvent is the engine-local mirror of notify.CoverEvent, kept
// here to avoid an import cycle. The api adapter translates this to
// the production event in step 13.
type CoverEvent struct {
	LeaveID         string
	LeaveMemberID   string
	LeaveMemberName string
	CoverMemberID   string
	CoverMemberName string
	StartDate       string
	EndDate         string
	ResolvedBy      string
}

var errMemberNotScheduled = errors.New("member not scheduled for this date")

func NewEngine(db *database.DB) *Engine {
	return &Engine{
		db:             db,
		holidayChecker: nil,
	}
}

// SetHolidayChecker sets a function that checks if dates are holidays.
func (e *Engine) SetHolidayChecker(checker HolidayChecker) {
	e.holidayChecker = checker
}

// SetNotifier wires a notifier that the engine calls after assigning
// covers. nil disables notifications; tests can omit the dependency.
func (e *Engine) SetNotifier(n CoverNotifier) {
	e.notifier = n
}

// GenerateSchedule creates round-robin assignments for a date range.
func (e *Engine) GenerateSchedule(ctx context.Context, startDate, endDate time.Time) error {
	members, err := e.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return err
	}

	if len(members) == 0 {
		return errors.New("no active team members")
	}

	currentDate := startDate

	for currentDate.Before(endDate.AddDate(0, 0, 1)) {
		if err := e.processDate(ctx, currentDate, members); err != nil {
			return err
		}
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	return nil
}

// processDate handles assignment for a single date. The R1 (original)
// member is read from the persisted R1 rotation state, not from an
// in-memory cursor — this is what lets schedule generation be split
// across multiple calls (EnsureSchedule, GenerateMissingDays,
// RegenerateSchedule) without losing the cursor's place.
//
// On a successful original-assignment write the R1 state is advanced
// to (currentDate, usedIndex), so the next business day's index
// will be one slot further. The advance is keyed off the actual
// write, not off processDate's call, which is the rule "R1 only
// advances when a new original assignment is written".
func (e *Engine) processDate(ctx context.Context, currentDate time.Time, members []database.TeamMember) error {
	// Skip weekends
	if currentDate.Weekday() == time.Saturday || currentDate.Weekday() == time.Sunday {
		return nil
	}

	// Skip holidays if holiday checker is configured
	if e.holidayChecker != nil && e.holidayChecker(currentDate) {
		return nil
	}

	dateStr := currentDate.Format("2006-01-02")
	leaves, err := e.db.GetLeaveByDate(ctx, dateStr)
	if err != nil {
		return err
	}

	r1Index, err := e.r1RotationIndex(ctx, currentDate, len(members))
	if err != nil {
		return err
	}
	originalMember := members[r1Index]
	coveringMember, err := e.determineCoveringMember(ctx, originalMember, leaves, members, currentDate)
	if err != nil {
		return err
	}

	if err := e.createAssignment(ctx, currentDate, dateStr, r1Index, originalMember, coveringMember); err != nil {
		return err
	}

	return nil
}

// determineCoveringMember finds who should cover the assignment.
// Uses independent R2 cover rotation for fair distribution.
//
// If the cover rotation state cannot be read (a real DB error, not
// the sql.ErrNoRows "no state yet" case which the state reader
// handles internally), the error is returned to the caller. The
// caller (processDate) then fails the whole schedule generation
// rather than silently producing a schedule where the on-leave
// member "covers" themselves.
func (e *Engine) determineCoveringMember(ctx context.Context, originalMember database.TeamMember, leaves []database.LeaveRecord, members []database.TeamMember, currentDate time.Time) (database.TeamMember, error) {
	for i := range leaves {
		if leaves[i].MemberID == originalMember.ID {
			// The cover rotation index is read from the persisted
			// cover_rotation_state row and advanced forward by the
			// number of working days since the last call. The first
			// call seeds the state at (currentDate, 0); subsequent
			// calls advance from there.
			coverIndex, err := e.coverRotationIndex(ctx, currentDate, len(members))
			if err != nil {
				return database.TeamMember{}, err
			}
			cover, coverErr := e.findCover(members, leaves, coverIndex)
			if coverErr == nil {
				return cover, nil
			}
			break
		}
	}
	return originalMember, nil
}

// createAssignment writes the rota assignment for one date and, on a
// successful original-assignment insert, advances the R1 rotation
// state to (currentDate, r1Index). The advance is the "only when a
// new original assignment is written" rule: a failed write leaves
// the state untouched so a retry can pick up at the same index.
//
// currentDate is the time.Time the engine was processing (used to
// stamp the new R1 state); dateStr is the same date formatted for
// the assignment row. r1Index is the member slot the caller chose
// from the persisted R1 state — it is what we record as the index
// for this date in the new state row.
func (e *Engine) createAssignment(
	ctx context.Context,
	currentDate time.Time,
	dateStr string,
	r1Index int,
	originalMember, coveringMember database.TeamMember,
) error {
	isCover := coveringMember.ID != originalMember.ID

	if isCover {
		// For cover assignments, we need to:
		// 1. Create the original assignment for the person on leave
		// 2. Create the cover assignment that references the original
		originalAssignmentID, err := e.db.CreateRotaAssignment(ctx, dateStr, originalMember.ID, false, nil)
		if err != nil {
			return err
		}
		// The original is on disk; advance R1 before the cover
		// write. If the cover write fails the next HandleLeaveChange
		// pass will re-create it, and the R1 state is already
		// correct (the original was written, so advancing was
		// correct).
		if advanceErr := e.advanceR1RotationState(ctx, currentDate, r1Index); advanceErr != nil {
			return advanceErr
		}

		// Create the cover assignment
		_, err = e.db.CreateRotaAssignment(ctx, dateStr, coveringMember.ID, true, &originalAssignmentID)
		return err
	}

	// For non-cover assignments, just create the original.
	if _, err := e.db.CreateRotaAssignment(ctx, dateStr, coveringMember.ID, false, nil); err != nil {
		return err
	}
	return e.advanceR1RotationState(ctx, currentDate, r1Index)
}

// advanceR1RotationState persists the R1 (last_date, last_index)
// pair. Truncates currentDate to midnight UTC so equality checks
// against the stored state work in the same coordinate space as
// the R2 rotation. Uses an INSERT-on-conflict-do-nothing to seed
// the row on the very first call: the row must exist (id = 1, set
// by the R2 path or by an earlier R1 write) but a concurrent
// first-call from the R2 path is fine — the second writer just
// overwrites with the same pair.
func (e *Engine) advanceR1RotationState(ctx context.Context, currentDate time.Time, index int) error {
	truncated := currentDate.UTC().Truncate(hoursPerDay * time.Hour)
	return e.db.UpsertR1RotationState(ctx, truncated, index)
}

// findCover returns members[startIndex] if they are not on leave, or the
// next available member cyclically after them. The new cover rotation
// index points directly at the person who should cover, so the search
// starts at startIndex (not startIndex+1) — the old "start after the R1"
// behavior is no longer needed because the rotation index is independent
// of who's on leave as the R1.
func (e *Engine) findCover(members []database.TeamMember, leaves []database.LeaveRecord, startIndex int) (database.TeamMember, error) {
	for i := range members {
		candidateIndex := (startIndex + i) % len(members)
		candidate := members[candidateIndex]

		// Check if candidate is on leave
		onLeave := false
		for j := range leaves {
			if leaves[j].MemberID == candidate.ID {
				onLeave = true
				break
			}
		}

		if !onLeave {
			return candidate, nil
		}
	}

	return database.TeamMember{}, errors.New("no available cover found")
}

// AssignCoversForLeave creates cover assignments for a leave record.
func (e *Engine) AssignCoversForLeave(ctx context.Context, leaveID string) error {
	leave, err := e.db.GetLeaveByID(ctx, leaveID)
	if err != nil {
		return err
	}

	// If leave is not active, nothing to do (reconciliation handles cleanup)
	if !isLeaveActive(leave.Status) {
		return nil
	}

	members, err := e.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return err
	}

	coverDates, err := e.processLeaveDates(ctx, leave, members, leaveID)
	if err != nil {
		return err
	}

	e.fireCoverAssigned(ctx, leave, members, coverDates)
	return nil
}

// fireCoverAssigned emits one CoverAssigned event per cover member,
// with the date range they were assigned. The notifier itself
// resolves the email address; the engine only knows the member_id
// and the dates.
func (e *Engine) fireCoverAssigned(ctx context.Context, leave *database.LeaveRecord, members []database.TeamMember, coverDates map[string][]string) {
	if e.notifier == nil || len(coverDates) == 0 {
		return
	}
	leaveName := memberNameByID(members, leave.MemberID)
	for coverID, dates := range coverDates {
		if len(dates) == 0 {
			continue
		}
		e.notifier.CoverAssigned(ctx, CoverEvent{
			LeaveID:         leave.ID,
			LeaveMemberID:   leave.MemberID,
			LeaveMemberName: leaveName,
			CoverMemberID:   coverID,
			CoverMemberName: memberNameByID(members, coverID),
			StartDate:       dates[0],
			EndDate:         dates[len(dates)-1],
		})
	}
}

// memberNameByID looks up a member name in a slice. Returns "" if
// not found. Used to build CoverEvent payloads without a DB lookup.
func memberNameByID(members []database.TeamMember, id string) string {
	for i := range members {
		if members[i].ID == id {
			return members[i].Name
		}
	}
	return ""
}

// getNextCoverIndex was removed when the cover rotation was switched
// to a date-derivable index (see coverRotationIndex). The old
// DB-anchored rotation was the source of the "same person always
// covers" bug: in DESC processing order, the only covers on the rota
// have date >= referenceDate, so getNextCoverIndex always fell
// through to "no prior cover" and findCover always returned
// members[0].

// processLeaveDates processes each day of leave and creates cover
// assignments. It returns a map from cover member_id to the list of
// dates (ascending) on which that member was assigned cover. Skipped
// dates (weekends, holidays, days with no cover available) are
// omitted. The caller uses the map to fire one CoverAssigned event
// per cover member with their full date range.
//
// The cover rotation index for each day is computed from the date
// alone (see coverRotationIndex), so there's no cross-day index to
// track — the old startIndex parameter is gone.
func (e *Engine) processLeaveDates(ctx context.Context, leave *database.LeaveRecord, members []database.TeamMember, leaveID string) (map[string][]string, error) {
	coverDates := make(map[string][]string)
	for d := leave.StartDate; d.Before(leave.EndDate.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		coverID, err := e.processLeaveDate(ctx, d, members, leave, leaveID)
		if err != nil {
			return nil, err
		}
		if coverID != "" {
			dateStr := d.Format("2006-01-02")
			coverDates[coverID] = append(coverDates[coverID], dateStr)
		}
	}
	return coverDates, nil
}

// shouldSkipDate checks if a date should be skipped (weekend or holiday).
func (e *Engine) shouldSkipDate(d time.Time) bool {
	// Skip weekends
	if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		return true
	}

	// Skip holidays if holiday checker is configured
	if e.holidayChecker != nil && e.holidayChecker(d) {
		return true
	}

	return false
}

// coverRotationIndex returns the cover rotation index for currentDate,
// using the persisted cover_rotation_state row as the anchor. The state
// stores the index for a specific date; for any later date, the new
// index is the old index plus the number of working days (weekdays
// that are not holidays) between them, modulo teamSize.
//
// The engine only ever queries for dates >= state.last_date, so the
// state always moves forward in time. On the first call after a fresh
// database, the state is initialized at (currentDate, 0) — the
// current date itself is the anchor, and the rotation advances from
// there. This means:
//   - No O(years_in_operation) walk on every call.
//   - Retroactive holiday changes only affect the delta from the last
//     stored state to the current date, not the entire history.
//
// If a caller ever queries for a date before the stored state (which
// should not happen — the engine only looks forward), we walk backward
// from the stored state without updating it. This is a safety net, not
// a normal code path.
func (e *Engine) coverRotationIndex(ctx context.Context, currentDate time.Time, teamSize int) (int, error) {
	if teamSize <= 0 {
		return 0, nil
	}
	currentDate = currentDate.UTC().Truncate(hoursPerDay * time.Hour)

	lastDate, lastIndex, err := e.db.GetCoverRotationState(ctx)
	if err != nil {
		// sql.ErrNoRows means no state yet — initialize at the
		// current date with index 0. Any other error is a real
		// failure and must surface.
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("read cover rotation state: %w", err)
		}
		if err := e.db.UpsertCoverRotationState(ctx, currentDate, 0); err != nil {
			return 0, fmt.Errorf("initialize cover rotation state: %w", err)
		}
		return 0, nil
	}

	lastDate = lastDate.UTC().Truncate(hoursPerDay * time.Hour)

	switch {
	case currentDate.Equal(lastDate):
		// Same date as the last computed index — return it directly.
		// modNonNegative (not the built-in %) so a corrupt
		// negative lastIndex in the DB can't produce a negative
		// candidate index for findCover.
		return modNonNegative(lastIndex, teamSize), nil

	case currentDate.After(lastDate):
		// Advance forward from the stored state. The delta is bounded
		// by how far the caller has moved since the last call (usually
		// one or two business days), not by the total age of the
		// installation.
		delta := e.workingDaysBetween(lastDate, currentDate)
		newIndex := modNonNegative(lastIndex+delta, teamSize)
		if err := e.db.UpsertCoverRotationState(ctx, currentDate, newIndex); err != nil {
			return 0, fmt.Errorf("update cover rotation state: %w", err)
		}
		return newIndex, nil

	default:
		// currentDate is before the stored state. The engine only
		// looks forward, so this should not happen in normal
		// operation. Walk backward from the stored state without
		// updating it — a later forward call will overwrite the state
		// with the correct forward-computed index.
		delta := e.workingDaysBetween(currentDate, lastDate)
		return modNonNegative(lastIndex-delta, teamSize), nil
	}
}

// r1RotationIndex returns the R1 (original-HAT) rotation index for
// currentDate, computed from the persisted R1 sub-state of
// cover_rotation_state. Unlike coverRotationIndex, this is a pure
// read: the state is NOT advanced as a side effect. Callers must
// persist the new state themselves (via UpsertR1RotationState)
// after they have actually written the corresponding original
// assignment — this is what gives the R1 rotation the property
// "only advances when a new original assignment is written". A
// failed write leaves the state untouched, so the next successful
// attempt picks up at the same index.
//
// The state shape is identical to R2's: a (last_date, last_index)
// pair. The index for a later date is the stored index plus the
// number of working days between, modulo teamSize. On the first
// call after a fresh database the R1 sub-state is null, so the
// function returns 0 (the first member in the alphabetical list)
// without persisting — the caller will write the state after the
// corresponding assignment lands.
//
// If a caller ever queries for a date before the stored state (which
// should not happen — the engine only looks forward), we walk
// backward from the stored state without updating it.
func (e *Engine) r1RotationIndex(ctx context.Context, currentDate time.Time, teamSize int) (int, error) {
	if teamSize <= 0 {
		return 0, nil
	}
	currentDate = currentDate.UTC().Truncate(hoursPerDay * time.Hour)

	lastDate, lastIndex, err := e.db.GetR1RotationState(ctx)
	if err != nil {
		// sql.ErrNoRows is expected on the very first call: the
		// R1 sub-state hasn't been seeded yet. Return index 0
		// without persisting — the caller writes the state after
		// the assignment lands.
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("read R1 rotation state: %w", err)
	}

	lastDate = lastDate.UTC().Truncate(hoursPerDay * time.Hour)

	switch {
	case currentDate.Equal(lastDate):
		return modNonNegative(lastIndex, teamSize), nil

	case currentDate.After(lastDate):
		delta := e.workingDaysBetween(lastDate, currentDate)
		return modNonNegative(lastIndex+delta, teamSize), nil

	default:
		delta := e.workingDaysBetween(currentDate, lastDate)
		return modNonNegative(lastIndex-delta, teamSize), nil
	}
}

// workingDaysBetween counts working days in [from, to). Both arguments
// are expected to be midnight-UTC dates. A working day is a weekday
// that is not a holiday under the configured holiday checker.
func (e *Engine) workingDaysBetween(from, to time.Time) int {
	count := 0
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		if e.shouldSkipDate(d) {
			continue
		}
		count++
	}
	return count
}

// modNonNegative returns x mod m in the range [0, m). Go's % operator
// returns a result with the sign of the dividend, which gives negative
// values for negative x; callers want a non-negative slot index.
func modNonNegative(x, m int) int {
	r := x % m
	if r < 0 {
		r += m
	}
	return r
}

// processLeaveDate handles a single day of leave and returns the
// cover member's id (empty if the day was skipped or no cover was
// found). The cover rotation index is computed from the date alone,
// so no startIndex is needed.
func (e *Engine) processLeaveDate(ctx context.Context, d time.Time, members []database.TeamMember, leave *database.LeaveRecord, leaveID string) (string, error) {
	if e.shouldSkipDate(d) {
		return "", nil
	}

	dateStr := d.Format("2006-01-02")
	originalAssignmentID, err := e.ensureOriginalAssignment(ctx, dateStr, leave)
	if errors.Is(err, errMemberNotScheduled) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	// Get all leave records for this date to exclude them from cover selection
	allLeaves, err := e.db.GetLeaveByDate(ctx, dateStr)
	if err != nil {
		return "", err
	}

	coverIndex, err := e.coverRotationIndex(ctx, d, len(members))
	if err != nil {
		return "", err
	}
	cover, err := e.findCover(members, allLeaves, coverIndex)
	if err != nil {
		// Skip if no cover available - this is intentional
		return "", nil //nolint:nilerr // no cover available is a valid outcome, not an error
	}

	if err := e.createCoverAssignment(ctx, dateStr, cover.ID, originalAssignmentID); err != nil {
		return "", err
	}

	if err := e.db.UpdateLeaveStatus(ctx, leaveID, "assigned"); err != nil {
		return "", err
	}

	return cover.ID, nil
}

// isLeaveActive returns true if the status still requires cover assignments.
func isLeaveActive(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return s == "pending" || s == "assigned"
}

// ensureOriginalAssignment finds or creates the original assignment for the person on leave.
func (e *Engine) ensureOriginalAssignment(ctx context.Context, dateStr string, leave *database.LeaveRecord) (string, error) {
	existingAssignments, err := e.db.GetAssignmentsByDate(ctx, dateStr)
	if err != nil {
		return "", err
	}

	// First check if the person on leave is the original assignment
	for _, a := range existingAssignments {
		if a.MemberID == leave.MemberID && !a.IsCover {
			return a.ID, nil
		}
	}

	// If the person on leave is the cover, find the original assignment
	for _, a := range existingAssignments {
		if a.MemberID == leave.MemberID && a.IsCover && a.OriginalAssignmentID != nil {
			// The cover person is taking leave - return the original assignment they were covering
			return *a.OriginalAssignmentID, nil
		}
	}

	// No assignment found for this person on this date
	if len(existingAssignments) > 0 {
		return "", errMemberNotScheduled
	}

	return "", errMemberNotScheduled
}

// createCoverAssignment creates or updates a cover assignment for the given
// date. It is idempotent:
//   - if a cover already exists with the same member, it is a no-op;
//   - if a cover exists with a different member, the member_id is updated
//     to point to the new person (e.g. when a cover themselves take leave);
//   - if no cover exists, a new one is created.
//
// This idempotency is required because AssignCoversForLeave is invoked from
// multiple paths (web form, API, manual reprocess, HandleLeaveChange
// reconcile) and any of them may run more than once for the same leave.
func (e *Engine) createCoverAssignment(ctx context.Context, dateStr, coverMemberID, originalAssignmentID string) error {
	// Check if there's already a cover assignment for this date.
	existingAssignments, err := e.db.GetAssignmentsByDate(ctx, dateStr)
	if err != nil {
		return err
	}

	for _, a := range existingAssignments {
		if !a.IsCover {
			continue
		}
		if a.MemberID == coverMemberID {
			// Cover already points at the right person; leave it alone.
			return nil
		}
		// Cover exists but with a different member (e.g. the original
		// cover themselves took leave). Re-point the cover in place.
		dateTime, parseErr := time.Parse("2006-01-02", dateStr)
		if parseErr != nil {
			return parseErr
		}

		query := `UPDATE rota_assignments SET member_id = ? WHERE date = ? AND is_cover = 1`
		_, err = e.db.ExecContext(ctx, query, coverMemberID, dateTime)
		return err
	}

	// No existing cover, create a new one.
	_, err = e.db.CreateRotaAssignment(ctx, dateStr, coverMemberID, true, &originalAssignmentID)
	return err
}
