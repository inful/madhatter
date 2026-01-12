package web

import (
	"context"
	"os"
	"time"

	"github.com/inful/madhatter/internal/notifications"
)

const (
	notificationKindTeamsOnCallTomorrow = "teams_oncall_tomorrow"
	teamsOnCallWebhookEnv               = "TEAMS_ONCALL_WEBHOOK_URL"
	teamsOnCallLookaheadDays            = 14
)

func (h *Handler) notifyTeamsTomorrowOnCall(ctx context.Context) {
	webhookURL := os.Getenv(teamsOnCallWebhookEnv)
	if webhookURL == "" {
		return
	}

	date, assignmentID, memberID, memberName, ok := h.getTomorrowOnCall(ctx)
	if !ok {
		return
	}

	message := "Tomorrow's on-call: " + memberName

	logID, created, err := h.db.TryCreateNotificationLog(ctx, notificationKindTeamsOnCallTomorrow, date, memberID, assignmentID, message)
	if err != nil {
		return
	}
	if !created {
		return
	}

	n := &notifications.TeamsWebhookNotifier{WebhookURL: webhookURL}
	if err := n.Send(ctx, message); err != nil {
		_ = h.db.DeleteNotificationLog(ctx, logID)
		return
	}
}

func (h *Handler) getTomorrowOnCall(ctx context.Context) (date string, assignmentID string, memberID string, memberName string, ok bool) {
	start := time.Now().AddDate(0, 0, 1)

	// Look ahead up to two weeks to find the next business day.
	for i := range teamsOnCallLookaheadDays {
		candidate := start.AddDate(0, 0, i)
		if !h.isBusinessDay(candidate) {
			continue
		}

		dateStr := candidate.Format("2006-01-02")
		assignments, err := h.db.GetAssignmentsByDate(ctx, dateStr)
		if err != nil || len(assignments) == 0 {
			continue
		}

		// Prefer cover assignment.
		best := assignments[0]
		for j := range assignments {
			if assignments[j].IsCover {
				best = assignments[j]
				break
			}
		}

		if best.MemberName == "" {
			continue
		}

		return dateStr, best.ID, best.MemberID, best.MemberName, true
	}

	return "", "", "", "", false
}
