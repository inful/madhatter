package web

import (
	"context"
	"html/template"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/calendar"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/notify"
	"github.com/inful/madhatter/internal/rota"
	"github.com/inful/madhatter/internal/wfh"
)

const (
	weekDaysCount                = 10
	weekDaysOffset               = 4
	schedulePrealloc             = 5
	defaultCalendarLookaheadDays = 90
	weekDaysInWeek               = 7
	defaultHolidayLookaheadDays  = 30
	maxStringLength              = 255
	dictKeyValuePairs            = 2
	schemeHTTPS                  = "https"
)

type Handler struct {
	db             *database.DB
	maintenance    *rota.ScheduleMaintenance
	tmpl           *template.Template
	router         *chi.Mux
	authManager    *auth.AuthManager
	authMiddleware *auth.Middleware
	holidayChecker func(time.Time) bool
	holidayLookup  calendar.HolidayLookup
	development    bool
	restoreMu      sync.Mutex
	restoreBusy    atomic.Bool
	pendingMu      sync.Mutex
	pendingRestore map[string]pendingRestoreItem
	wfhService     *wfh.Service
	notifier       notify.Notifier
}

type pendingRestoreItem struct {
	Path      string
	CreatedAt time.Time
}

type presenceDay struct {
	DateISO          string
	DateDisplay      string
	IsToday          bool
	Assigned         *database.TeamMember
	AssignedSwapped  bool
	AssignedSwapInfo string
	Present          []database.TeamMember
	WFH              []database.TeamMember
	Away             []presenceLeave
}

type presenceLeave struct {
	Member database.TeamMember
}

type scheduleMatrix struct {
	Days []scheduleMatrixDay
	Rows []scheduleMatrixRow
}

type scheduleMatrixDay struct {
	DateISO     string
	DateDisplay string
	IsToday     bool
	AtWorkCount int
	WFHCount    int
	LeaveCount  int
}

type scheduleMatrixRow struct {
	Member database.TeamMember
	Cells  []scheduleMatrixCell
}

type scheduleMatrixCell struct {
	Status    string
	Label     string
	Assigned  bool
	Swapped   bool
	SwapInfo  string
	IsToday   bool
	DateISO   string
	DateLabel string
}

func NewHandler(db *database.DB, authManager *auth.AuthManager, authMiddleware *auth.Middleware, development bool, holidayChecker func(time.Time) bool) (*Handler, error) {
	// Parse templates.
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}

	router := chi.NewRouter()
	maintenance := rota.NewScheduleMaintenance(db)
	if holidayChecker != nil {
		maintenance.SetHolidayChecker(holidayChecker)
	}

	h := &Handler{
		db:             db,
		maintenance:    maintenance,
		tmpl:           tmpl,
		router:         router,
		authManager:    authManager,
		authMiddleware: authMiddleware,
		holidayChecker: holidayChecker,
		development:    development,
		pendingRestore: make(map[string]pendingRestoreItem),
	}

	h.registerRoutes()

	// Register development-specific routes if in development mode.
	if development {
		h.registerDevelopmentRoutes()
	}

	return h, nil
}

// SetWFHService sets the WFH service on the handler.
func (h *Handler) SetWFHService(svc *wfh.Service) {
	h.wfhService = svc
}

// SetNotifier wires the notification dispatcher. Handlers call into
// it after state changes that should email a user. nil is treated as
// "no notifier wired" — handlers tolerate this so tests can omit the
// dependency.
func (h *Handler) SetNotifier(n notify.Notifier) {
	h.notifier = n
}

// notifierOrNil returns the installed notifier, or a no-op when none
// is wired. The no-op satisfies the notify.Notifier interface but
// drops every event, so handlers can call it unconditionally.
func (h *Handler) notifierOrNil() notify.Notifier {
	if h.notifier == nil {
		return notifyNoop{}
	}
	return h.notifier
}

// SetHolidayLookup wires a holiday lookup that returns the holiday
// name for a given date. Used by the calendar package's per-day
// presence snapshot. nil is allowed (every date is non-holiday).
func (h *Handler) SetHolidayLookup(l calendar.HolidayLookup) {
	h.holidayLookup = l
}

// notifyNoop is the no-op notifier used when a handler is constructed
// without a real Notifier (e.g. in some unit tests). It exists so
// handlers can call h.notifierOrNil().SwapXxx(...) without nil checks
// at every call site.
type notifyNoop struct{}

func (notifyNoop) SwapRequested(_ context.Context, _ notify.SwapEvent)  {}
func (notifyNoop) SwapAccepted(_ context.Context, _ notify.SwapEvent)   {}
func (notifyNoop) SwapRejected(_ context.Context, _ notify.SwapEvent)   {}
func (notifyNoop) SwapCancelled(_ context.Context, _ notify.SwapEvent)  {}
func (notifyNoop) WFHStateChanged(_ context.Context, _ notify.WFHEvent) {}
func (notifyNoop) CoverAssigned(_ context.Context, _ notify.CoverEvent) {}
