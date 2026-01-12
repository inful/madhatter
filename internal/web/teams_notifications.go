package web

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/inful/madhatter/internal/notifications"
)

const (
	notificationKindTeamsOnCallTomorrow = "teams_oncall_tomorrow"
	teamsOnCallWebhookEnv               = "TEAMS_ONCALL_WEBHOOK_URL"
	teamsOnCallLookaheadDays            = 14
	teamsOnCallTickInterval             = 1 * time.Hour
)

var teamsOnCallNotifierOnce sync.Once

func (h *Handler) startTeamsOnCallNotifier(ctx context.Context) {
	if os.Getenv(teamsOnCallWebhookEnv) == "" {
		return
	}

	teamsOnCallNotifierOnce.Do(func() {
		go func() {
			// Best-effort initial attempt.
			if err := h.notifyTeamsTomorrowOnCall(ctx); err != nil {
				log.Printf("Teams notifier: %v\n", err)
			}

			ticker := time.NewTicker(teamsOnCallTickInterval)
			defer ticker.Stop()
			for range ticker.C {
				if err := h.notifyTeamsTomorrowOnCall(ctx); err != nil {
					log.Printf("Teams notifier: %v\n", err)
				}
			}
		}()
	})
}

func (h *Handler) notifyTeamsTomorrowOnCall(ctx context.Context) error {
	webhookURL := os.Getenv(teamsOnCallWebhookEnv)
	if webhookURL == "" {
		return nil
	}

	date, assignmentID, memberID, memberName, ok := h.getTomorrowOnCall(ctx)
	if !ok {
		return nil
	}

	message := "Tomorrow's on-call: " + memberName

	logID, created, err := h.db.TryCreateNotificationLog(ctx, notificationKindTeamsOnCallTomorrow, date, memberID, assignmentID, message)
	if err != nil {
		return err
	}
	if !created {
		return nil
	}

	n := &notifications.TeamsWebhookNotifier{WebhookURL: webhookURL}
	if err := n.Send(ctx, message); err != nil {
		_ = h.db.DeleteNotificationLog(ctx, logID)
		return err
	}

	return nil
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
