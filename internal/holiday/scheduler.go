package holiday

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	dailyInterval = 24 * time.Hour
)

// Scheduler manages the background job for fetching holidays from iCal URLs.
type Scheduler struct {
	store     *Store
	fetcher   holidayFetcher
	urls      []string
	running   bool
	stopChan  chan struct{}
	wg        sync.WaitGroup
	interval  time.Duration
	lastFetch time.Time
	lastError error
	mu        sync.RWMutex
}

// holidayFetcher is the subset of ICalFetcher the scheduler needs. Defined as an interface
// so tests can substitute a stub that avoids HTTP I/O.
type holidayFetcher interface {
	FetchMultiple(ctx context.Context, urls []string) ([]Holiday, error)
}

// NewScheduler creates a new holiday scheduler.
func NewScheduler(store *Store, urls []string) *Scheduler {
	return &Scheduler{
		store:    store,
		fetcher:  NewICalFetcher(),
		urls:     urls,
		stopChan: make(chan struct{}),
		interval: dailyInterval, // Default: run daily
	}
}

// Start begins the background scheduler.
func (s *Scheduler) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("scheduler is already running")
	}
	s.running = true
	s.mu.Unlock()

	log.Printf("Starting holiday scheduler with %d URL(s)\n", len(s.urls))

	// Run immediately on start
	s.wg.Go(func() {
		s.runFetch(context.Background())
	})

	// Start periodic fetcher
	s.wg.Go(func() {
		s.periodicFetch()
	})

	return nil
}

// Stop gracefully stops the background scheduler.
func (s *Scheduler) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return errors.New("scheduler is not running")
	}
	s.running = false
	s.mu.Unlock()

	log.Println("Stopping holiday scheduler...")
	close(s.stopChan)
	s.wg.Wait()
	log.Println("Holiday scheduler stopped")
	return nil
}

// periodicFetch runs the fetch operation at regular intervals.
func (s *Scheduler) periodicFetch() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.wg.Go(func() {
				s.runFetch(context.Background())
			})
		}
	}
}

// runFetch performs a single fetch operation.
func (s *Scheduler) runFetch(ctx context.Context) {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	log.Println("Running holiday fetch...")

	holidays, err := s.fetcher.FetchMultiple(ctx, s.urls)

	s.mu.Lock()
	s.lastFetch = time.Now()
	s.lastError = err
	s.mu.Unlock()

	if err != nil {
		// Check if it's a partial success
		if len(holidays) > 0 {
			log.Printf("Warning: Partial success fetching holidays: %v\n", err)
			s.store.UpdateHolidays(holidays)
			log.Printf("Updated holidays: %d events loaded\n", len(holidays))
		} else {
			log.Printf("Error fetching holidays: %v\n", err)
			// Don't clear existing holidays on error
		}
		return
	}

	s.store.UpdateHolidays(holidays)
	log.Printf("Successfully updated holidays: %d events loaded\n", len(holidays))
}

// ForceFetch manually triggers a fetch operation.
func (s *Scheduler) ForceFetch(ctx context.Context) error {
	s.mu.RLock()
	if !s.running {
		s.mu.RUnlock()
		return errors.New("scheduler is not running")
	}
	s.mu.RUnlock()

	s.wg.Go(func() {
		s.runFetch(ctx)
	})

	return nil
}

// GetStatus returns the current status of the scheduler.
func (s *Scheduler) GetStatus() SchedulerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return SchedulerStatus{
		Running:      s.running,
		LastFetch:    s.lastFetch,
		LastError:    s.lastError,
		URLCount:     len(s.urls),
		HolidayCount: s.store.GetCount(),
	}
}

// UpdateURLs updates the list of iCal URLs to fetch from.
func (s *Scheduler) UpdateURLs(urls []string) {
	s.mu.Lock()
	s.urls = urls
	s.mu.Unlock()
}

// SetInterval sets the fetch interval (for testing purposes).
func (s *Scheduler) SetInterval(interval time.Duration) {
	s.mu.Lock()
	s.interval = interval
	s.mu.Unlock()
}

// SchedulerStatus represents the current status of the scheduler.
type SchedulerStatus struct {
	Running      bool      `json:"running"`
	LastFetch    time.Time `json:"last_fetch"`
	LastError    error     `json:"last_error"`
	URLCount     int       `json:"url_count"`
	HolidayCount int       `json:"holiday_count"`
}

// LoadHolidayURLsFromEnv loads holiday URLs from environment variable.
func LoadHolidayURLsFromEnv() []string {
	envValue := os.Getenv("HOLIDAY_URLS")
	if envValue == "" {
		return []string{}
	}

	// Split by comma and trim whitespace
	parts := strings.Split(envValue, ",")
	urls := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			urls = append(urls, trimmed)
		}
	}

	return urls
}

// ValidateHolidayURL validates that a URL is properly formatted.
func ValidateHolidayURL(url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("URL cannot be empty")
	}

	// Check if it looks like a URL (basic check)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return errors.New("URL must start with http:// or https://")
	}

	return nil
}

// FetchAndStoreImmediate fetches holidays immediately and stores them.
// This is useful for initial setup or manual refresh.
func (s *Scheduler) FetchAndStoreImmediate() ([]Holiday, error) {
	ctx := context.Background()
	holidays, err := s.fetcher.FetchMultiple(ctx, s.urls)
	if err != nil {
		return nil, err
	}

	s.store.UpdateHolidays(holidays)
	return holidays, nil
}

// GetFetchLog returns a summary of recent fetch activity.
func (s *Scheduler) GetFetchLog() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.lastFetch.IsZero() {
		return "No fetches performed yet"
	}

	status := fmt.Sprintf("Last fetch: %s", s.lastFetch.Format("2006-01-02 15:04:05"))
	if s.lastError != nil {
		status += fmt.Sprintf(" - Error: %v", s.lastError)
	} else {
		status += " - Success"
	}
	status += fmt.Sprintf(" - Holidays: %d", s.store.GetCount())

	return status
}
