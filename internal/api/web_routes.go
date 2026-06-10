package api

import (
	"os"
	"time"

	"github.com/inful/madhatter/internal/notify"
	"github.com/inful/madhatter/internal/web"
)

func (s *Server) registerWebRoutes(development bool) error {
	// Create web handler with auth components and holiday checker.
	var holidayChecker func(time.Time) bool
	if s.holidayService != nil {
		holidayChecker = s.holidayService.ShouldSkipDate
	}

	webHandler, err := web.NewHandler(s.db, s.authManager, s.authMiddleware, development, holidayChecker)
	if err != nil {
		return err
	}

	if s.wfhService != nil {
		webHandler.SetWFHService(s.wfhService)
		webHandler.SetHolidayLookup(web.NewHolidayLookup(s.holidayService))
	}
	if s.notifier != nil {
		webHandler.SetNotifier(s.notifier)
	}

	// Wire the unsubscribe plumbing when SESSION_SECRET is set.
	// The same secret signs session cookies; reusing it for
	// unsubscribe tokens keeps the key-management surface to one
	// place. PublicBaseURL is operator-configured so the resulting
	// URLs in emails match the host users actually visit.
	if secret := os.Getenv("SESSION_SECRET"); secret != "" {
		webHandler.SetUnsubscribeSecret(secret)
		if s.notifier != nil {
			// cfg.PublicBaseURL is loaded inside buildNotifier; we
			// re-read it here so the web layer's URL factory
			// matches the one the renderer used.
			cfg := notify.LoadConfigFromEnv()
			webHandler.SetPublicBaseURL(cfg.PublicBaseURL)
		}
	}

	// Development mode: The web handler's registerDevelopmentRoutes will handle the fake login view.
	// No need to register it separately here.

	// Mount web routes.
	s.router.Mount("/", webHandler.Router())
	return nil
}
