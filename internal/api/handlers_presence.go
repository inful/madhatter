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
	present, away := buildPresenceLists(memberMap, leaveRecords)

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
	return resp, nil
}

func mapMembersByID(members []database.TeamMember) map[string]database.TeamMember {
	memberMap := make(map[string]database.TeamMember, len(members))
	for _, m := range members {
		memberMap[m.ID] = m
	}
	return memberMap
}

func buildPresenceLists(memberMap map[string]database.TeamMember, leaveRecords []database.LeaveRecord) (present []database.TeamMember, away []database.TeamMember) {
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

	present = make([]database.TeamMember, 0, len(memberMap)-len(onLeave))
	for id, member := range memberMap {
		if _, absent := onLeave[id]; absent {
			continue
		}
		present = append(present, member)
	}

	sort.Slice(present, func(i, j int) bool { return present[i].Name < present[j].Name })
	sort.Slice(away, func(i, j int) bool { return away[i].Name < away[j].Name })

	return present, away
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
