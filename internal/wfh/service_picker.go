package wfh

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/inful/madhatter/internal/database"
)

// Picker-internal type used by orderByPickerPriority. The picker
// keys off three fields: periodWFHCount (lowest wins), score
// (lowest wins), and the member's name (alphabetical tiebreaker).
//
// Lives next to AssignWFHForDate so anyone touching the picker
// path has the scoring type in the same file.
type pickerScored struct {
	m              database.TeamMember
	periodWFHCount int
	score          int
}

// AssignWFHForDate is the scheduler entry point for the seat-cap
// picker. It runs only on weekdays that are not holidays, only
// when the picker is feature-on AND assignment-on AND has a
// positive seat cap. All three early-exit guards live on the
// method below so the orchestrator stays under the cyclomatic
// budget.
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
// extracted from AssignWFHForDate to keep the orchestrator under
// the cyclomatic-complexity budget. The early-exit guards
// (picker-disabled, past-date, weekend/holiday) live in
// AssignWFHForDate; once those pass, this method does the actual
// work.
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
// assignment-off, or has no seat cap configured.
func (s *Service) pickerDisabled() bool {
	return !s.cfg.Enabled || !s.cfg.AssignmentEnabled || s.cfg.SeatCap <= 0
}

// isPickerActiveOnDate returns false for holidays and weekends
// (the cap is meaningless when nobody is scheduled on-site) and
// true otherwise.
func (s *Service) isPickerActiveOnDate(date time.Time) bool {
	if s.db.IsHoliday(date) {
		return false
	}
	return date.Weekday() != time.Saturday && date.Weekday() != time.Sunday
}

// logCapShortFall emits the structured slog.Warn for a picker
// run that can't meet the cap.
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
// status='approved' wfh_requests rows and fires the WFHEvent for
// each so the assigned member is notified. Idempotent on UNIQUE
// collisions (concurrent picker races surface as
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
// existing wfh_requests row for the date.
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
//   - periodWFHCount: voluntary WFHs in the current period. Uses
//     GetWFHRequestsVoluntaryInPeriod (filters origin != 'assigned')
//     so Assigned WFHs don't burn quota — matches the user-facing
//     promise.
//   - score: co-presence tiebreaker. Higher score = the picker
//     keeps the candidate on-site more. Lower score = picked first.
//     Scored in calendar days since the candidate's last co-presence
//     with the would-be on-site cohort (the picker computes
//     onSiteCohort before sorting). NULL → horizon_days + 1
//     (history-clamp sentinel — see section 4 of
//     plans/assigned-wfh-plan.md, "Sentinel and history-clamp").
//   - name: deterministic alphabetical tiebreaker.
//
// Co-presence is gated on cfg.CoPresenceEnabled. When false, every
// candidate scores horizon_days + 1 (the sentinel) and the picker
// degenerates to (periodWFHCount, name).
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

// coPresenceScore returns the calendar-days-since-last-co-presence
// score for candidateID against cohortIDs, with the history-clamp
// applied. Returns horizon+1 when:
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
// The alphabetical tiebreaker keeps the picker deterministic across
// re-runs and across the scheduler / on-demand trigger paths.
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
// "members getting on-site today" — every member who COULD be on-site.
// That includes the candidates themselves (the picker hasn't decided
// yet whether to assign them WFH), capped at coPresenceCohortCap to
// fit the SQL's fixed IN-list placeholder count.
func (s *Service) pickerCohortIDs(ctx context.Context, date string, _ []database.TeamMember) ([]string, error) {
	members, err := s.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, err
	}
	cohortIDs := make([]string, 0, len(members))
	onLeaveIDs, err := s.leaveMemberIDsForDate(ctx, date)
	if err != nil {
		return nil, err
	}
	permanentIDs := permanentWFHMemberIDs(members)
	for i := range members {
		if _, onLeave := onLeaveIDs[members[i].ID]; onLeave {
			continue
		}
		if _, permanent := permanentIDs[members[i].ID]; permanent {
			continue
		}
		cohortIDs = append(cohortIDs, members[i].ID)
		if len(cohortIDs) >= coPresenceCohortCap {
			break
		}
	}
	return cohortIDs, nil
}
