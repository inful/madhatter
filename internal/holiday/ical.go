package holiday

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
)

// ICalFetcher handles fetching and parsing iCal feeds from remote URLs.
type ICalFetcher struct {
	client *http.Client
}

// NewICalFetcher creates a new iCal fetcher with a default HTTP client.
func NewICalFetcher() *ICalFetcher {
	return &ICalFetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
			// Follow redirects automatically
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return nil
			},
		},
	}
}

// FetchAndParse fetches an iCal feed from a URL and parses it into holidays.
func (f *ICalFetcher) FetchAndParse(url string) ([]Holiday, error) {
	// Fetch the iCal content
	resp, err := f.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch iCal from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the iCal content
	return ParseICalContent(string(body))
}

// ParseICalContent parses iCal content and extracts holidays.
func ParseICalContent(content string) ([]Holiday, error) {
	// Trim any leading/trailing whitespace
	content = strings.TrimSpace(content)
	
	if content == "" {
		return nil, fmt.Errorf("empty content")
	}

	// Try the standard library parser first
	reader := bytes.NewReader([]byte(content))
	cal, err := ics.ParseCalendar(reader)
	if err != nil {
		// If standard parser fails, try fallback parser
		return parseICalContentFallback(content)
	}

	events := cal.Events()
	holidays := make([]Holiday, 0, len(events))

	for _, event := range events {
		holiday, err := extractHolidayFromEvent(event)
		if err != nil {
			// Skip invalid events but log for debugging
			continue
		}
		holidays = append(holidays, holiday)
	}

	// If no events found, try fallback
	if len(holidays) == 0 {
		return parseICalContentFallback(content)
	}

	return holidays, nil
}

// parseICalContentFallback provides a fallback parser for malformed iCal content.
func parseICalContentFallback(content string) ([]Holiday, error) {
	var holidays []Holiday
	
	// Split content into lines
	lines := strings.Split(content, "\n")
	
	inEvent := false
	var currentEvent map[string]string
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		// Skip empty lines and non-iCal lines
		if line == "" {
			continue
		}
		
		// Check for calendar boundaries
		if strings.HasPrefix(line, "BEGIN:VCALENDAR") ||
		   strings.HasPrefix(line, "VERSION:") ||
		   strings.HasPrefix(line, "PRODID:") {
			continue
		}
		
		if line == "BEGIN:VEVENT" {
			inEvent = true
			currentEvent = make(map[string]string)
			continue
		}
		
		if line == "END:VEVENT" {
			inEvent = false
			if holiday, err := parseEventFallback(currentEvent); err == nil {
				holidays = append(holidays, holiday)
			}
			continue
		}
		
		if inEvent && strings.Contains(line, ":") {
			// Parse key:value format
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				currentEvent[key] = value
			}
		}
	}
	
	if len(holidays) == 0 {
		return nil, fmt.Errorf("no valid holidays found in content")
	}
	
	return holidays, nil
}

// parseEventFallback parses an event from a simple key-value map.
func parseEventFallback(event map[string]string) (Holiday, error) {
	// Get summary (holiday name)
	summary, ok := event["SUMMARY"]
	if !ok || summary == "" {
		return Holiday{}, fmt.Errorf("no summary")
	}
	
	// Get DTSTART
	dtStart, ok := event["DTSTART"]
	if !ok || dtStart == "" {
		// Try with VALUE=DATE suffix
		dtStart, ok = event["DTSTART;VALUE=DATE"]
		if !ok || dtStart == "" {
			return Holiday{}, fmt.Errorf("no start date")
		}
	}
	
	// Parse the date
	parsedDate, err := parseICalDate(dtStart)
	if err != nil {
		return Holiday{}, fmt.Errorf("invalid date: %w", err)
	}
	
	// Validate the date
	if err := ValidateHolidayDate(parsedDate); err != nil {
		return Holiday{}, fmt.Errorf("invalid holiday date: %w", err)
	}
	
	return Holiday{
		Date: parsedDate,
		Name: sanitizeHolidayName(summary),
	}, nil
}

// extractHolidayFromEvent extracts a holiday from an iCal event.
func extractHolidayFromEvent(event *ics.VEvent) (Holiday, error) {
	// Get event summary (holiday name)
	summary := event.GetProperty(ics.ComponentPropertySummary)
	if summary == nil || summary.Value == "" {
		return Holiday{}, fmt.Errorf("event has no summary")
	}

	name := summary.Value

	// Get event date - try DTSTART first, then DTSTART;VALUE=DATE
	var dateStr string
	
	dtStart := event.GetProperty(ics.ComponentPropertyDtStart)
	if dtStart != nil {
		// Parse the date
		dateStr = dtStart.Value
	}

	// If DTSTART is not available or invalid, try to get from other properties
	if dateStr == "" {
		return Holiday{}, fmt.Errorf("event has no start date")
	}

	// Parse the date to ensure it's valid and convert to YYYY-MM-DD format
	parsedDate, err := parseICalDate(dateStr)
	if err != nil {
		return Holiday{}, fmt.Errorf("invalid date format %s: %w", dateStr, err)
	}

	// Validate the date
	if err := ValidateHolidayDate(parsedDate); err != nil {
		return Holiday{}, fmt.Errorf("invalid holiday date: %w", err)
	}

	return Holiday{
		Date: parsedDate,
		Name: sanitizeHolidayName(name),
	}, nil
}

// parseICalDate parses various iCal date formats and returns YYYY-MM-DD.
func parseICalDate(dateStr string) (string, error) {
	// Handle different iCal date formats:
	// 1. YYYYMMDD (all-day event)
	// 2. YYYYMMDDTHHMMSSZ (with time, UTC)
	// 3. YYYYMMDDTHHMMSS (with time, no timezone)
	// 4. YYYY-MM-DD (already in our format)

	// Try simple format first
	if len(dateStr) == 10 && dateStr[4] == '-' && dateStr[7] == '-' {
		return dateStr, nil
	}

	// Parse YYYYMMDD format
	if len(dateStr) >= 8 {
		yearStr := dateStr[0:4]
		monthStr := dateStr[4:6]
		dayStr := dateStr[6:8]

		// Validate it's all digits
		for _, c := range yearStr + monthStr + dayStr {
			if c < '0' || c > '9' {
				return "", fmt.Errorf("invalid characters in date")
			}
		}

		return fmt.Sprintf("%s-%s-%s", yearStr, monthStr, dayStr), nil
	}

	return "", fmt.Errorf("unrecognized date format: %s", dateStr)
}

// sanitizeHolidayName cleans up the holiday name for display.
func sanitizeHolidayName(name string) string {
	// Remove extra whitespace
	name = strings.TrimSpace(name)
	
	// Replace multiple spaces with single space
	name = strings.Join(strings.Fields(name), " ")
	
	return name
}

// FetchMultiple fetches holidays from multiple URLs and combines them.
func (f *ICalFetcher) FetchMultiple(urls []string) ([]Holiday, error) {
	allHolidays := make([]Holiday, 0)
	errors := make([]error, 0)

	for _, url := range urls {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}

		holidays, err := f.FetchAndParse(url)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to fetch %s: %w", url, err))
			continue
		}

		allHolidays = append(allHolidays, holidays...)
	}

	// If we got some holidays but had some errors, return the holidays with partial success
	if len(allHolidays) > 0 && len(errors) > 0 {
		return allHolidays, fmt.Errorf("partial success: got %d holidays but had %d errors: %v", 
			len(allHolidays), len(errors), errors)
	}

	// If no holidays were fetched and there were errors, return the first error
	if len(allHolidays) == 0 && len(errors) > 0 {
		return nil, errors[0]
	}

	return allHolidays, nil
}

// FilterHolidaysByYear filters holidays to include only those within the specified years.
func FilterHolidaysByYear(holidays []Holiday, years ...int) []Holiday {
	if len(years) == 0 {
		return holidays
	}

	yearSet := make(map[int]bool)
	for _, y := range years {
		yearSet[y] = true
	}

	filtered := make([]Holiday, 0)
	for _, h := range holidays {
		hDate, err := time.Parse("2006-01-02", h.Date)
		if err != nil {
			continue
		}
		if yearSet[hDate.Year()] {
			filtered = append(filtered, h)
		}
	}

	return filtered
}

// FilterHolidaysByDateRange filters holidays to include only those within the specified date range.
func FilterHolidaysByDateRange(holidays []Holiday, startDate, endDate string) ([]Holiday, error) {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, fmt.Errorf("invalid start date: %w", err)
	}

	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, fmt.Errorf("invalid end date: %w", err)
	}

	filtered := make([]Holiday, 0)
	for _, h := range holidays {
		hDate, err := time.Parse("2006-01-02", h.Date)
		if err != nil {
			continue
		}
		if (hDate.Equal(start) || hDate.After(start)) && (hDate.Equal(end) || hDate.Before(end)) {
			filtered = append(filtered, h)
		}
	}

	return filtered, nil
}