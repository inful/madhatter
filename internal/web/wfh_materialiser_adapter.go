package web

import (
	"context"
	"time"

	"github.com/inful/madhatter/internal/calendar"
	"github.com/inful/madhatter/internal/wfh"
)

// wfhMaterialiserAdapter implements calendar.WFHMaterialiser against
// the wfh.Service. The adapter calls EnsureRecurringMaterialized so
// the calendar snapshot sees the same recurring rows the WFH feature
// does.
type wfhMaterialiserAdapter struct {
	svc *wfh.Service
}

func (a wfhMaterialiserAdapter) EnsureRecurringMaterialized(ctx context.Context, start, end time.Time) (int, error) {
	if a.svc == nil {
		return 0, nil
	}
	return a.svc.EnsureRecurringMaterialized(ctx, start, end)
}

// NewWFHMaterialiser returns a calendar.WFHMaterialiser backed by svc.
// Returns nil when svc is nil so the web layer can pass it through
// unconditionally.
func NewWFHMaterialiser(svc *wfh.Service) calendar.WFHMaterialiser {
	if svc == nil {
		return nil
	}
	return wfhMaterialiserAdapter{svc: svc}
}

// wfhAssignerAdapter implements calendar.AssignWFHAssigner against
// the wfh.Service. Step 9 of plans/assigned-wfh-plan.md: the
// calendar's RefreshFor runs the picker once per day before the
// snapshot is built, so the calendar shows freshly-assigned rows.
// nil-safe so the web layer can pass it through unconditionally.
type wfhAssignerAdapter struct {
	svc *wfh.Service
}

func (a wfhAssignerAdapter) AssignWFHForDate(ctx context.Context, date string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.AssignWFHForDate(ctx, date)
}

// NewWFHAssigner returns a calendar.AssignWFHAssigner backed by
// svc. Returns nil when svc is nil so the web layer can pass it
// through unconditionally.
func NewWFHAssigner(svc *wfh.Service) calendar.AssignWFHAssigner {
	if svc == nil {
		return nil
	}
	return wfhAssignerAdapter{svc: svc}
}
