package api

import (
	"os"

	"github.com/inful/madhatter/internal/notify"
	"github.com/inful/madhatter/internal/web"
)

func (s *Server) registerWebRoutes(development bool) error {
	cfg := web.HandlerConfig{
		DB:             s.db,
		AuthManager:    s.authManager,
		AuthMiddleware: s.authMiddleware,
		Development:    development,
		Notifier:       s.notifier,
		WFHService:     s.wfhService,
	}
	if s.holidayService != nil {
		cfg.HolidayChecker = s.holidayService.ShouldSkipDate
		cfg.HolidayLookup = web.NewHolidayLookup(s.holidayService)
	}

	// Unsubscribe plumbing reads two env vars at startup. The
	// HandlerConfig wires both together; supplying one without
	// the other is intentional inert rather than building a
	// half-broken URL factory.
	if secret := os.Getenv("SESSION_SECRET"); secret != "" && s.notifier != nil {
		cfg.UnsubscribeSecret = secret
		cfg.PublicBaseURL = notify.LoadConfigFromEnv().PublicBaseURL
	}

	webHandler, err := web.NewHandlerConfig(cfg)
	if err != nil {
		return err
	}

	// Mount web routes.
	s.router.Mount("/", webHandler.Router())
	return nil
}
