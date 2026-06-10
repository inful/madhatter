package web

import "github.com/inful/madhatter/internal/holiday"

// holidayServiceAdapter implements calendar.HolidayLookup against a
// holiday.Service. The adapter only uses the GetHoliday lookup so the
// calendar package can see the holiday name in the snapshot.
type holidayServiceAdapter struct {
	svc *holiday.Service
}

func (a holidayServiceAdapter) GetHoliday(dateStr string) (string, bool) {
	if a.svc == nil {
		return "", false
	}
	h, ok := a.svc.GetHoliday(dateStr)
	if !ok {
		return "", false
	}
	return h.Name, true
}

// NewHolidayLookup returns a calendar.HolidayLookup backed by svc.
// Returns nil when svc is nil so the web layer can pass it through
// unconditionally.
func NewHolidayLookup(svc *holiday.Service) *holidayServiceAdapter {
	if svc == nil {
		return nil
	}
	return &holidayServiceAdapter{svc: svc}
}
