package notify

import (
	"fmt"
	"strings"
)

// renderEvent is the lightweight renderer used by LogNotifier. The
// production outbox path uses the same payload shape but writes the
// (subject, body) into the outbox row, where the worker later picks
// it up; the worker doesn't need to re-render.
//
// Step 5 replaces this with a text/template-based renderer that is
// also env-overridable, matching the calendar template pattern
// documented in AGENTS.md.
func renderEvent(eventKind string, event any) (subject, body string, err error) {
	switch e := event.(type) {
	case SwapEvent:
		return renderSwap(eventKind, e)
	case WFHEvent:
		return renderWFH(e)
	case CoverEvent:
		return renderCover(e)
	default:
		return "", "", fmt.Errorf("notify: unknown event type %T", event)
	}
}

func renderSwap(kind string, e SwapEvent) (subject, body string, err error) {
	switch kind {
	case EventSwapRequested:
		subject = fmt.Sprintf("HAT swap request from %s", e.RequesterName)
		body = fmt.Sprintf(
			"%s has proposed swapping HAT days with you.\n\nTheir HAT day: %s\nYour HAT day:  %s\n\nLog in to accept or reject: %%BASE_URL%%/swaps\n",
			e.RequesterName, e.RequesterDate, e.TargetDate,
		)
	case EventSwapAccepted:
		subject = fmt.Sprintf("Swap accepted: you and %s", e.TargetName)
		body = fmt.Sprintf(
			"Your swap with %s has been accepted. The rota has been updated.\n\n%s's day: %s\nYour day:    %s\n",
			e.TargetName, e.RequesterName, e.RequesterDate, e.TargetDate,
		)
	case EventSwapRejected:
		subject = fmt.Sprintf("Swap rejected by %s", e.TargetName)
		body = fmt.Sprintf(
			"%s has rejected your swap request.\n\nTheir HAT day: %s\nYour HAT day:  %s\n",
			e.TargetName, e.Targeter(), e.RequesterDate,
		)
		if e.Reason != "" {
			body += "\nReason: " + e.Reason + "\n"
		}
	case EventSwapCancelled:
		subject = fmt.Sprintf("Swap cancelled by %s", e.RequesterName)
		body = fmt.Sprintf(
			"%s has cancelled the swap request.\n\nTheir HAT day: %s\nYour HAT day:  %s\n",
			e.RequesterName, e.RequesterDate, e.TargetDate,
		)
	default:
		return "", "", fmt.Errorf("notify: unknown swap kind %q", kind)
	}
	return subject, body, nil
}

func renderWFH(e WFHEvent) (subject, body string, err error) {
	subject = fmt.Sprintf("WFH request %s", strings.ToLower(e.NewStatus))
	body = fmt.Sprintf(
		"Your Work-From-Home request for %s has been %s.\n\n",
		e.Date, e.NewStatus,
	)
	if e.OldStatus != "" {
		body += fmt.Sprintf("Status: %s -> %s\n", e.OldStatus, e.NewStatus)
	}
	if e.ActorName != "" {
		body += "Action by: " + e.ActorName + "\n"
	}
	body += "\nLog in to view: %BASE_URL%/wfh\n"
	return subject, body, nil
}

func renderCover(e CoverEvent) (subject, body string, err error) {
	subject = fmt.Sprintf("You are covering for %s", e.LeaveMemberName)
	body = fmt.Sprintf(
		"You have been assigned to cover the rota for %s from %s to %s.\n\nLog in to view: %%BASE_URL%%/\n",
		e.LeaveMemberName, e.StartDate, e.EndDate,
	)
	return subject, body, nil
}

// Requester is a tiny helper used by renderSwap to keep the rejection
// wording clean. It returns the requester's HAT-day date as a string;
// for now we expose it on the event so the renderer can reference it
// without re-deriving the field name.
func (e SwapEvent) Requester() string { return e.RequesterDate }

// Targeter returns the target's HAT-day date string.
func (e SwapEvent) Targeter() string { return e.TargetDate }
