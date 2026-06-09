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
