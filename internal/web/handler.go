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
	"github.com/inful/madhatter/internal/ratelimit"
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

	// defaultAuthRateLimit is the bucket capacity for the OAuth
	// login route. 10 requests per minute per IP is enough for
	// normal use (a person clicking the wrong button, refreshing)
	// and tight enough that brute-forcing a code or token would take
	// years.
	defaultAuthRateLimit  = 10
	defaultAuthRateRefill = 10.0 / 60.0 // 10 tokens / 60s ≈ 0.1667 tokens/s
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

	// Per-IP rate limiter used by the OAuth login route. Defaults
	// to 10 requests per minute per IP; tests can swap a smaller
	// bucket in via SetRateLimiters to exercise the 429 path.
	authRateLimiter *ratelimit.Limiter

	// Unsubscribe plumbing. The secret is shared with the renderer
	// and the email channel via unsubscribeURLFn so URLs minted
	// for the email body and the List-Unsubscribe header come from
	// the same key. publicBaseURL is the externally-visible origin
	// (e.g. https://rota.example.com); when empty, unsubscribe
	// URLs are suppressed in templates.
	unsubscribeSecret string
	publicBaseURL     string
	unsubscribeURLFn  func(memberID string) string
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
	// AdminMarkedMemberIDs is the set of member IDs whose WFH today
	// was recorded by an admin via /admin/wfh/mark (the override
	// path), as opposed to a self-requested WFH. The schedule matrix
	// uses this to render admin-marked cells in the purple-blue
	// chip color, distinct from the user-requested WFH color.
	AdminMarkedMemberIDs map[string]struct{}
}

type presenceLeave struct {
	Member    database.TeamMember
	LeaveType string // empty for plain leave; "conference" for conference leave
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
	// AtWFHFloor is true when the day's at-work count has reached
	// the WFH minimum-onsite floor (the larger of MinOnsiteAbsolute
	// and the percentage of active members, rounded up). When set,
	// the column is at-capacity for additional WFH — the WFH icon
	// in the column header is rendered in an orange tone to flag
	// it visually.
	AtWFHFloor bool
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
	// LeaveType is the leave_records.leave_type value when Status is
	// "away". Empty when the member is not on leave. The template
	// uses it to pick the per-cell icon and tag color so conference
	// leaves are visually distinct from plain leave.
	LeaveType string
	// IsAdminMarkedWFH is true when the cell is WFH and the row's
	// WFH for this date was recorded by an admin via /admin/wfh/mark.
	// The template uses it to swap the chip's Bulma class from
	// is-info (blue) to is-link (purple-blue).
	IsAdminMarkedWFH bool
}

// HandlerConfig is the wired-dependency bundle NewHandlerConfig
// consumes. Fields are optional except DB, AuthManager, and
// AuthMiddleware; the constructor defaults the rest (rate limiter,
// authRateRefill, no-op notifier) so the most common shape —
// `NewHandlerConfig(HandlerConfig{DB: db, AuthManager: am, AuthMiddleware: mw})`
// is enough to get a fully-routable Handler in tests.
//
// The Config is the additive counterpart to NewHandler (kept for
// back-compat until every caller migrates). After cut-over, late-
// bound setters on *Handler can be deleted; today they coexist.
type HandlerConfig struct {
	DB             *database.DB
	AuthManager    *auth.AuthManager
	AuthMiddleware *auth.Middleware

	Development    bool
	HolidayChecker func(time.Time) bool

	// HolidayLookup powers per-day presence snapshots in the
	// calendar/ical package. nil = every date is non-holiday.
	HolidayLookup calendar.HolidayLookup

	// Notifier is the dispatcher handlers reach via notifierOrNil.
	// nil is replaced with a no-op adapter so handlers can
	// unconditionally dispatch without nil checks at every callsite.
	Notifier notify.Notifier
}

// NewHandlerConfig builds a Handler from a wired dependency bundle.
// See HandlerConfig for field semantics. Use this in new code; the
// positional NewHandler is the back-compat shape and will be
// deleted once all callers migrate (tracked by a follow-up
// commit).
func NewHandlerConfig(cfg HandlerConfig) (*Handler, error) {
	h, err := NewHandler(
		cfg.DB,
		cfg.AuthManager,
		cfg.AuthMiddleware,
		cfg.Development,
		cfg.HolidayChecker,
	)
	if err != nil {
		return nil, err
	}
	if cfg.Notifier != nil {
		h.notifier = cfg.Notifier
	} else {
		// Default to a no-op adapter so handlers can dispatch
		// unconditionally without nil checks at every call site.
		// Migration away from notifyNoop happens alongside the
		// setter removal in a follow-up commit.
		h.notifier = notifyNoop{}
	}
	if cfg.HolidayLookup != nil {
		h.holidayLookup = cfg.HolidayLookup
	}
	return h, nil
}

func NewHandler(db *database.DB, authManager *auth.AuthManager, authMiddleware *auth.Middleware, development bool, holidayChecker func(time.Time) bool) (*Handler, error) {
	// Parse templates.
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}

	router := chi.NewRouter()
	// Apply defensive HTTP headers to every response. Mounted first
	// so it covers auth routes, the API mount, and the static asset
	// handlers alike.
	router.Use(securityHeadersMiddleware)
	// Vendored third-party assets (HTMX, Bulma, FontAwesome). Local
	// URLs let the strict CSP keep default-src 'self' without
	// exception; see internal/web/static.go and security_headers.go.
	router.Handle("/static/*", staticHandler())
	maintenance := rota.NewScheduleMaintenance(db)
	if holidayChecker != nil {
		maintenance.SetHolidayChecker(holidayChecker)
	}

	h := &Handler{
		db:              db,
		maintenance:     maintenance,
		tmpl:            tmpl,
		router:          router,
		authManager:     authManager,
		authMiddleware:  authMiddleware,
		holidayChecker:  holidayChecker,
		development:     development,
		pendingRestore:  make(map[string]pendingRestoreItem),
		authRateLimiter: ratelimit.New(defaultAuthRateLimit, defaultAuthRateRefill),
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

func (notifyNoop) SwapRequested(_ context.Context, _ notify.SwapEvent)                      {}
func (notifyNoop) SwapAccepted(_ context.Context, _ notify.SwapEvent)                       {}
func (notifyNoop) SwapRejected(_ context.Context, _ notify.SwapEvent)                       {}
func (notifyNoop) SwapCancelled(_ context.Context, _ notify.SwapEvent)                      {}
func (notifyNoop) WFHStateChanged(_ context.Context, _ notify.WFHEvent)                     {}
func (notifyNoop) CoverAssigned(_ context.Context, _ notify.CoverEvent)                     {}
func (notifyNoop) UserPendingApproval(_ context.Context, _ notify.UserPendingApprovalEvent) {}
