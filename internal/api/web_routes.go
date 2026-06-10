package api

import (
	"time"

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

	// Development mode: The web handler's registerDevelopmentRoutes will handle the fake login view.
	// No need to register it separately here.

	// Mount web routes.
	s.router.Mount("/", webHandler.Router())
	return nil
}
