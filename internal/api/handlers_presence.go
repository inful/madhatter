package api

import (
	"context"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
)

type GetPresenceTodayOutput struct {
	Body struct {
		Date           string                `json:"date"`
		Support        *database.TeamMember  `json:"support,omitempty"`
		SupportIsCover bool                  `json:"support_is_cover"`
		Present        []database.TeamMember `json:"present"`
		Away           []database.TeamMember `json:"away"`
		WFH            []database.TeamMember `json:"wfh"`
	}
}

func (s *Server) handleGetPresenceToday(ctx context.Context, input *struct{}) (*GetPresenceTodayOutput, error) {
	_ = input

	// Check authentication.
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}
	if _, ok := auth.GetUserFromContext(ctx); !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	dateStr := time.Now().Format("2006-01-02")

	members, err := s.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get team members", err)
	}
	memberMap := mapMembersByID(members)

	leaveRecords, err := s.db.GetLeaveByDate(ctx, dateStr)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get leave records", err)
	}

	wfhRequests, err := s.db.GetWFHRequestsByDateAndStatus(ctx, dateStr, database.WFHStatusApproved)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get WFH requests", err)
	}

	present, away, wfhMembers := buildPresenceListsWithWFH(memberMap, leaveRecords, wfhRequests)

	assignments, err := s.db.GetAssignmentsByDate(ctx, dateStr)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get assignments", err)
	}
	support, supportIsCover := selectSupportMember(assignments, memberMap)

	resp := &GetPresenceTodayOutput{}
	resp.Body.Date = dateStr
	resp.Body.Support = support
	resp.Body.SupportIsCover = supportIsCover
	resp.Body.Present = present
	resp.Body.Away = away
	resp.Body.WFH = wfhMembers
	return resp, nil
}

func mapMembersByID(members []database.TeamMember) map[string]database.TeamMember {
	memberMap := make(map[string]database.TeamMember, len(members))
	for _, m := range members {
		memberMap[m.ID] = m
	}
	return memberMap
}

// buildPresenceListsWithWFH partitions active members into present (on-site), away (on leave),
// and WFH (approved work-from-home) lists for a given day.
func buildPresenceListsWithWFH(
	memberMap map[string]database.TeamMember,
	leaveRecords []database.LeaveRecord,
	wfhRequests []database.WFHRequest,
) (present, away, wfhList []database.TeamMember) {
	onLeave := make(map[string]struct{}, len(leaveRecords))
	away = make([]database.TeamMember, 0, len(leaveRecords))
	for i := range leaveRecords {
		member, ok := memberMap[leaveRecords[i].MemberID]
		if !ok {
			continue
		}
		onLeave[leaveRecords[i].MemberID] = struct{}{}
		away = append(away, member)
	}

	onWFH := make(map[string]struct{}, len(wfhRequests))
	wfhList = make([]database.TeamMember, 0, len(wfhRequests))
	for i := range wfhRequests {
		member, ok := memberMap[wfhRequests[i].MemberID]
		if !ok {
			continue
		}
		onWFH[wfhRequests[i].MemberID] = struct{}{}
		wfhList = append(wfhList, member)
	}

	present = make([]database.TeamMember, 0, len(memberMap))
	for id, member := range memberMap {
		if _, absent := onLeave[id]; absent {
			continue
		}
		if _, remote := onWFH[id]; remote {
			continue
		}
		present = append(present, member)
	}

	sort.Slice(present, func(i, j int) bool { return present[i].Name < present[j].Name })
	sort.Slice(away, func(i, j int) bool { return away[i].Name < away[j].Name })
	sort.Slice(wfhList, func(i, j int) bool { return wfhList[i].Name < wfhList[j].Name })

	return present, away, wfhList
}

func selectSupportMember(assignments []database.RotaAssignment, memberMap map[string]database.TeamMember) (*database.TeamMember, bool) {
	// Prioritize cover assignment.
	for i := range assignments {
		if !assignments[i].IsCover {
			continue
		}
		m, ok := memberMap[assignments[i].MemberID]
		if !ok {
			continue
		}
		return &m, true
	}

	// Fall back to original assignment.
	for i := range assignments {
		if assignments[i].IsCover {
			continue
		}
		m, ok := memberMap[assignments[i].MemberID]
		if !ok {
			continue
		}
		return &m, false
	}

	return nil, false
}
