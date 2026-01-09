package holiday

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/inful/madhatter/internal/database"
)

// Service is the main holiday service that integrates holiday management with the rota system.
type Service struct {
	store     *Store
	scheduler *Scheduler
	mu        sync.RWMutex
}

// NewService creates a new holiday service.
func NewService(db *database.DB) *Service {
	store := NewStore()
	urls := LoadHolidayURLsFromEnv()
	scheduler := NewScheduler(store, urls)

	return &Service{
		store:     store,
		scheduler: scheduler,
	}
}

// Start initializes and starts the holiday service.
func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Start the scheduler if URLs are configured
	urls := s.scheduler.urls
	if len(urls) == 0 {
		log.Println("No holiday URLs configured. Set HOLIDAY_URLS environment variable to enable holiday support.")
		return nil
	}

	// Validate URLs
	for _, url := range urls {
		if err := ValidateHolidayURL(url); err != nil {
			return fmt.Errorf("invalid holiday URL %s: %w", url, err)
		}
	}

	// Start the scheduler
	if err := s.scheduler.Start(); err != nil {
		return fmt.Errorf("failed to start holiday scheduler: %w", err)
	}

	log.Printf("Holiday service started with %d URL(s)\n", len(urls))
	return nil
}

// Stop gracefully stops the holiday service.
func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.scheduler != nil {
		return s.scheduler.Stop()
	}
	return nil
}

// IsHoliday checks if a given date is a holiday.
func (s *Service) IsHoliday(dateStr string) bool {
	return s.store.IsHoliday(dateStr)
}

// GetHoliday returns the holiday for a specific date.
func (s *Service) GetHoliday(dateStr string) (Holiday, bool) {
	return s.store.GetHoliday(dateStr)
}

// GetUpcomingHolidays returns upcoming holidays within the specified days.
func (s *Service) GetUpcomingHolidays(daysAhead int) []Holiday {
	return s.store.GetUpcomingHolidays(daysAhead)
}

// GetAllHolidays returns all holidays in the store.
func (s *Service) GetAllHolidays() []Holiday {
	return s.store.GetAllHolidays()
}

// ForceRefresh manually triggers a holiday refresh.
func (s *Service) ForceRefresh(ctx context.Context) error {
	if s.scheduler == nil {
		return errors.New("scheduler not initialized")
	}
	return s.scheduler.ForceFetch(ctx)
}

// GetStatus returns the current status of the holiday service.
func (s *Service) GetStatus() ServiceStatus {
	status := ServiceStatus{
		HolidayCount: s.store.GetCount(),
	}

	if s.scheduler != nil {
		schedulerStatus := s.scheduler.GetStatus()
		status.SchedulerRunning = schedulerStatus.Running
		status.LastFetch = schedulerStatus.LastFetch
		status.LastError = schedulerStatus.LastError
		status.URLCount = schedulerStatus.URLCount
	}

	return status
}

// UpdateURLs updates the holiday URLs and restarts the scheduler.
func (s *Service) UpdateURLs(urls []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.scheduler != nil {
		// Stop existing scheduler
		if err := s.scheduler.Stop(); err != nil {
			return fmt.Errorf("failed to stop scheduler: %w", err)
		}
	}

	// Create new scheduler with updated URLs
	s.scheduler = NewScheduler(s.store, urls)

	// Start the new scheduler
	if len(urls) > 0 {
		if err := s.scheduler.Start(); err != nil {
			return fmt.Errorf("failed to start scheduler: %w", err)
		}
	}

	return nil
}

// ShouldSkipDate checks if a date should be skipped from scheduling.
// This includes weekends and holidays.
func (s *Service) ShouldSkipDate(date time.Time) bool {
	// Check weekends
	if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		return true
	}

	// Check holidays
	dateStr := date.Format("2006-01-02")
	return s.IsHoliday(dateStr)
}

// GetSkipReason returns the reason why a date should be skipped.
func (s *Service) GetSkipReason(date time.Time) string {
	if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		return "Weekend"
	}

	dateStr := date.Format("2006-01-02")
	if holiday, exists := s.GetHoliday(dateStr); exists {
		return fmt.Sprintf("Holiday: %s", holiday.Name)
	}

	return ""
}

// GetHolidaysForDateRange returns holidays within a date range.
func (s *Service) GetHolidaysForDateRange(startDate, endDate string) ([]Holiday, error) {
	return FilterHolidaysByDateRange(s.GetAllHolidays(), startDate, endDate)
}

// ServiceStatus represents the status of the holiday service.
type ServiceStatus struct {
	SchedulerRunning bool      `json:"scheduler_running"`
	LastFetch        time.Time `json:"last_fetch"`
	LastError        error     `json:"last_error"`
	URLCount         int       `json:"url_count"`
	HolidayCount     int       `json:"holiday_count"`
}

// GetEnvironmentConfig returns the current environment configuration.
func GetEnvironmentConfig() map[string]string {
	urls := LoadHolidayURLsFromEnv()
	return map[string]string{
		"HOLIDAY_URLS": fmt.Sprintf("%d URLs configured", len(urls)),
		"URLS":         fmt.Sprintf("%v", urls),
	}
}

// InitializeHolidayService creates and starts the holiday service.
// This is called during application startup.
func InitializeHolidayService(db *database.DB) (*Service, error) {
	service := NewService(db)

	// Start the service
	if err := service.Start(); err != nil {
		return nil, fmt.Errorf("failed to initialize holiday service: %w", err)
	}

	return service, nil
}

// MustInitializeHolidayService is like InitializeHolidayService but panics on error.
func MustInitializeHolidayService(db *database.DB) *Service {
	service, err := InitializeHolidayService(db)
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize holiday service: %v", err))
	}
	return service
}
