# Dynamic Holiday Support Implementation

## Overview

This implementation adds comprehensive holiday support to the support rota system, allowing holidays to be loaded from iCal URL subscriptions and automatically excluded from rota assignments.

## Architecture

### Core Components

1. **Holiday Store** (`internal/holiday/store.go`)
   - In-memory cache for holiday dates
   - Thread-safe operations
   - Fast O(1) date lookups
   - Stores only dates (no raw iCal data)

2. **iCal Parser** (`internal/holiday/ical.go`)
   - Parses iCal feeds from URLs
   - Handles recurring events
   - Extracts future holidays (current + next year)
   - Graceful error handling

3. **Holiday Service** (`internal/holiday/service.go`)
   - Coordinates store, parser, and scheduler
   - Provides holiday checker function
   - Manages multiple URLs
   - Exposes status and refresh APIs

4. **Background Scheduler** (`internal/holiday/scheduler.go`)
   - Nightly fetch job (2 AM UTC)
   - Automatic refresh on startup
   - Error logging with last-known data preservation

5. **Rota Engine Integration** (`internal/rota/engine.go`)
   - Holiday checker function
   - Skips holidays during assignment
   - Prevents shift creation on holidays

6. **Web Interface** (`internal/web/`)
   - Calendar views show holiday badges
   - Dashboard displays upcoming holidays
   - Visual indicators (purple badges, background highlights)

## Configuration

### Environment Variables

```bash
# Comma-separated list of iCal URLs
HOLIDAY_URLS=https://www.officeholidays.com/subscribe/norway,https://www.officeholidays.com/subscribe/uk

# Optional: Custom fetch interval (hours)
HOLIDAY_FETCH_INTERVAL=24

# Optional: Custom lookahead days
HOLIDAY_LOOKAHEAD=365
```

### Example iCal URLs

- **OfficeHolidays**: `https://www.officeholidays.com/subscribe/{country}`
- **TimeAndDate**: `https://www.timeanddate.com/calendar/ics.html?year={year}&country={country}`
- **Google Calendar**: Public calendar ICS URLs

## How It Works

### 1. Initialization

```go
// In api/server.go
holidayService, err := holiday.InitializeHolidayService(db)
if err != nil {
    log.Printf("Warning: Failed to initialize holiday service: %v\n", err)
    holidayService = nil
}

// Set holiday checker on engine
engine := rota.NewEngine(db)
if holidayService != nil {
    engine.SetHolidayChecker(holidayService.ShouldSkipDate)
}
```

### 2. Nightly Fetch Process

1. **Scheduler triggers** at 2 AM UTC
2. **Fetches iCal feeds** from configured URLs
3. **Parses events** and extracts dates
4. **Updates store** with new holiday dates
5. **Logs errors** but continues with last known data

### 3. Schedule Generation

When generating schedules:

```go
// In rota/engine.go
func (e *Engine) GenerateSchedule(ctx context.Context, start, end time.Time) error {
    for d := start; d.Before(end.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
        // Skip weekends
        if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
            continue
        }
        
        // Skip holidays
        if e.holidayChecker != nil && e.holidayChecker(d) {
            continue
        }
        
        // Create assignment...
    }
}
```

### 4. Web Interface Display

**Dashboard** (`dashboard.html`):
- Shows upcoming holidays card
- Lists holidays with dates
- Purple styling for visibility

**Schedule View** (`schedule_current.html`):
- Holiday dates marked with purple badge
- "HOLIDAY" indicator in date header
- No assignments shown on holidays
- Distinct background gradient

**Calendar Day Display**:
```html
<div class="calendar-day holiday">
    <div class="date-header">
        <div class="holiday-indicator">HOLIDAY</div>
    </div>
    <div class="assignments">
        <div class="empty-state">
            <i class="fas fa-holly-berry"></i> Holiday
        </div>
    </div>
</div>
```

## API Endpoints

### Holiday Management

```http
GET /api/v1/holidays
# Returns upcoming holidays

GET /api/v1/holidays/status
# Returns scheduler status, last fetch, error count

POST /api/v1/holidays/refresh
# Manually trigger holiday refresh
```

### Response Examples

```json
{
  "holidays": [
    {"date": "2026-01-01", "name": "New Year's Day"},
    {"date": "2026-12-25", "name": "Christmas Day"}
  ],
  "message": "Found 2 upcoming holidays"
}
```

```json
{
  "scheduler_running": true,
  "last_fetch": "2026-01-09 02:00:00",
  "last_error": null,
  "url_count": 2,
  "holiday_count": 15
}
```

## Error Handling

### Graceful Degradation

1. **Startup failures**: Service continues without holidays
2. **Fetch failures**: Uses last known data, logs errors
3. **Parse failures**: Skips malformed events, continues
4. **Network issues**: Retry on next scheduled fetch

### Error Logging

```
2026-01-09 02:00:00 ERROR: Failed to fetch holiday URL https://example.com/ical: timeout
2026-01-09 02:00:00 INFO: Using last known holiday data (15 holidays)
```

## Performance Considerations

### Memory Usage
- Store holds only dates (not full iCal data)
- ~100 bytes per holiday
- 365 holidays = ~36KB

### Lookup Performance
- O(1) date lookup using map
- No database queries needed
- Fast schedule generation

### Network Efficiency
- Nightly fetches only (not per-request)
- Parallel URL fetching
- Connection pooling

## Testing

All existing tests pass with holiday integration:

```bash
# Run all tests
go test ./...

# Specific holiday tests
go test ./internal/holiday -v

# Integration tests
go test ./internal/api -v
go test ./internal/web -v
go test ./internal/rota -v
```

## Deployment Notes

### Environment Setup

```bash
# Required for holiday support
export HOLIDAY_URLS="https://www.officeholidays.com/subscribe/norway"

# Optional customizations
export HOLIDAY_FETCH_INTERVAL="24"  # hours
export HOLIDAY_LOOKAHEAD="365"      # days
```

### Monitoring

- Check logs for "ERROR: Failed to fetch holiday"
- Monitor `/api/v1/holidays/status` endpoint
- Verify calendar displays show holiday badges

### Troubleshooting

1. **No holidays showing**: Check `HOLIDAY_URLS` environment variable
2. **Fetch failures**: Verify URLs are accessible from server
3. **Schedule gaps**: Check that holidays are being excluded
4. **UI issues**: Verify template changes are deployed

## Future Enhancements

1. **Holiday names**: Store and display actual holiday names from iCal
2. **Country-specific**: Support different countries per team member
3. **Manual overrides**: Admin UI to add/remove holidays
4. **Holiday categories**: Different types (public, optional, company)
5. **ICS export**: Include holidays in calendar subscriptions

## Files Modified

- `internal/holiday/store.go` - New
- `internal/holiday/ical.go` - New
- `internal/holiday/service.go` - New
- `internal/holiday/scheduler.go` - New
- `internal/rota/engine.go` - Added holiday checker
- `internal/api/server.go` - Integrated holiday service
- `internal/web/handlers.go` - Added holiday data to templates
- `internal/web/templates/dashboard.html` - Added holiday card
- `internal/web/templates/schedule_current.html` - Added holiday styling

## Compatibility

- **Backward compatible**: Works without holidays configured
- **No database changes**: Uses in-memory store
- **Template safe**: Graceful handling of missing holiday data
- **API compatible**: Existing endpoints unchanged