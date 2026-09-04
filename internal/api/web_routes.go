package api

import (
	"os"

	"github.com/inful/madhatter/internal/notify"
	"github.com/inful/madhatter/internal/web"
)

func (s *Server) registerWebRoutes(development bool) error {
	// Build the wiring bundle for the web handler.
	cfg := web.HandlerConfig{
		DB:             s.db,
		AuthManager:    s.authManager,
		AuthMiddleware: s.authMiddleware,
		Development:    development,
		Notifier:       s.notifier,
	}
	// HolidayChecker is a tiny adapter around holidayService when
	// present. Pass nil when the service is absent and the
	// maintenance loop will treat every day as a working day.
	if s.holidayService != nil {
		cfg.HolidayChecker = s.holidayService.ShouldSkipDate
	}
	if s.wfhService != nil && s.holidayService != nil {
		cfg.HolidayLookup = web.NewHolidayLookup(s.holidayService)
	}

	webHandler, err := web.NewHandlerConfig(cfg)
	if err != nil {
		return err
	}

	// WFHService is a late-bound dependency that the constructor
	// does not (yet) take. Set it after construction today;
	// follow-up commits hoist it onto HandlerConfig too.
	if s.wfhService != nil {
		webHandler.SetWFHService(s.wfhService)
	}

	// Unsubscribe plumbing is also late-bound today because the
	// SESSION_SECRET and NOTIFY_PUBLIC_BASE_URL come from the
	// environment and are read at startup. Same migration path
	// as WFHService.
	if secret := os.Getenv("SESSION_SECRET"); secret != "" {
		webHandler.SetUnsubscribeSecret(secret)
		if s.notifier != nil {
			// cfg.PublicBaseURL is loaded inside buildNotifier;
			// re-read it here so the web layer's URL factory
			// matches the one the renderer used.
			notifyCfg := notify.LoadConfigFromEnv()
			webHandler.SetPublicBaseURL(notifyCfg.PublicBaseURL)
		}
	}

	// Mount web routes.
	s.router.Mount("/", webHandler.Router())
	return nil
}
