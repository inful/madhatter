package web

import (
	"html/template"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
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
	development    bool
	restoreMu      sync.Mutex
	restoreBusy    atomic.Bool
	pendingMu      sync.Mutex
	pendingRestore map[string]pendingRestoreItem
	wfhService     *wfh.Service
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
