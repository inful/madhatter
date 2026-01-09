package holiday

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	yearsAhead = 2
)

// Holiday represents a holiday event.
type Holiday struct {
	Date string `json:"date"` // YYYY-MM-DD format
	Name string `json:"name"`
}

// Store manages holidays in memory with thread-safe operations.
type Store struct {
	mu       sync.RWMutex
	holidays map[string]Holiday // date -> Holiday
}

// NewStore creates a new holiday store.
func NewStore() *Store {
	return &Store{
		holidays: make(map[string]Holiday),
	}
}

// UpdateHolidays replaces all holidays with the provided list.
func (s *Store) UpdateHolidays(holidays []Holiday) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.holidays = make(map[string]Holiday)
	for _, h := range holidays {
		s.holidays[h.Date] = h
	}
}

// IsHoliday checks if a given date is a holiday.
func (s *Store) IsHoliday(dateStr string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.holidays[dateStr]
	return exists
}

// GetHoliday returns the holiday for a specific date, if it exists.
func (s *Store) GetHoliday(dateStr string) (Holiday, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	h, exists := s.holidays[dateStr]
	return h, exists
}

// GetUpcomingHolidays returns holidays within the specified number of days from today.
func (s *Store) GetUpcomingHolidays(daysAhead int) []Holiday {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	endDate := now.AddDate(0, 0, daysAhead)

	var result []Holiday
	for _, h := range s.holidays {
		hDate, err := time.Parse("2006-01-02", h.Date)
		if err != nil {
			continue
		}
		if hDate.After(now) && hDate.Before(endDate) {
			result = append(result, h)
		}
	}

	// Sort by date using efficient sort
	sort.Slice(result, func(i, j int) bool {
		return result[i].Date < result[j].Date
	})

	return result
}

// GetAllHolidays returns all holidays in the store.
func (s *Store) GetAllHolidays() []Holiday {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Holiday, 0, len(s.holidays))
	for _, h := range s.holidays {
		result = append(result, h)
	}
	return result
}

// Clear removes all holidays from the store.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.holidays = make(map[string]Holiday)
}

// GetCount returns the number of holidays in the store.
func (s *Store) GetCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.holidays)
}

// ValidateHolidayDate checks if a date string is valid and within the supported range.
func ValidateHolidayDate(dateStr string) error {
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return fmt.Errorf("invalid date format: %s, expected YYYY-MM-DD", dateStr)
	}

	// Check if date is not too far in the past (more than 1 year ago)
	oneYearAgo := time.Now().AddDate(-1, 0, 0)
	if date.Before(oneYearAgo) {
		return fmt.Errorf("date %s is more than 1 year in the past", dateStr)
	}

	// Check if date is not too far in the future (more than 2 years ahead)
	twoYearsAhead := time.Now().AddDate(yearsAhead, 0, 0)
	if date.After(twoYearsAhead) {
		return fmt.Errorf("date %s is more than 2 years in the future", dateStr)
	}

	return nil
}
