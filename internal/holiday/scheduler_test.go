package holiday

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// iCalServer returns an httptest.Server serving sampleICal. Tests should rely
// on the automatic server.Close() registered via t.Cleanup.
func iCalServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(sampleICal))
	}))
	t.Cleanup(server.Close)
	return server
}

// errorServer returns a server that responds with the given status code.
func errorServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", status)
	}))
	t.Cleanup(server.Close)
	return server
}

const sampleICal = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
DTSTART:20260615
SUMMARY:Test Holiday
END:VEVENT
END:VCALENDAR`

func TestScheduler_StartStop(t *testing.T) {
	store := NewStore()
	sched := NewScheduler(store, nil)
	t.Cleanup(func() { _ = sched.Stop() })

	require.NoError(t, sched.Start())
	assert.True(t, sched.GetStatus().Running)

	require.NoError(t, sched.Stop())
	assert.False(t, sched.GetStatus().Running)
}

func TestScheduler_StartTwiceFails(t *testing.T) {
	store := NewStore()
	sched := NewScheduler(store, nil)
	t.Cleanup(func() { _ = sched.Stop() })

	require.NoError(t, sched.Start())
	err := sched.Start()
	assert.Error(t, err)
}

func TestScheduler_StopWhenNotRunning(t *testing.T) {
	store := NewStore()
	sched := NewScheduler(store, nil)
	err := sched.Stop()
	assert.Error(t, err)
}

func TestScheduler_ForceFetchWhenNotRunning(t *testing.T) {
	store := NewStore()
	sched := NewScheduler(store, nil)
	err := sched.ForceFetch(context.Background())
	assert.Error(t, err)
}

func TestScheduler_StartPerformsInitialFetch(t *testing.T) {
	urls := []string{iCalServer(t).URL}
	store := NewStore()
	sched := NewScheduler(store, urls)
	t.Cleanup(func() { _ = sched.Stop() })

	require.NoError(t, sched.Start())

	require.Eventually(t, func() bool {
		status := sched.GetStatus()
		return !status.LastFetch.IsZero() && status.HolidayCount > 0
	}, 2*time.Second, 10*time.Millisecond)

	status := sched.GetStatus()
	require.NoError(t, status.LastError)
	assert.Equal(t, 1, status.HolidayCount)
	assert.Equal(t, 1, status.URLCount)
}

func TestScheduler_FetchErrorPreservesExistingHolidays(t *testing.T) {
	server := errorServer(t, http.StatusNotFound)
	store := NewStore()
	store.UpdateHolidays([]Holiday{{Date: "2026-01-01", Name: "Existing"}})

	sched := NewScheduler(store, []string{server.URL})
	t.Cleanup(func() { _ = sched.Stop() })

	require.NoError(t, sched.Start())
	require.Eventually(t, func() bool {
		return sched.GetStatus().LastError != nil
	}, 2*time.Second, 10*time.Millisecond)

	assert.Equal(t, 1, store.GetCount(), "existing holidays must be preserved on fetch error")
}

func TestScheduler_FetchErrorWithoutPriorData(t *testing.T) {
	store := NewStore()
	sched := NewScheduler(store, []string{errorServer(t, http.StatusInternalServerError).URL})
	t.Cleanup(func() { _ = sched.Stop() })

	require.NoError(t, sched.Start())
	require.Eventually(t, func() bool {
		return sched.GetStatus().LastError != nil
	}, 2*time.Second, 10*time.Millisecond)

	assert.Equal(t, 0, store.GetCount(), "store stays empty on a hard error with no prior data")
}

func TestScheduler_ForceFetchWhileRunning(t *testing.T) {
	server := iCalServer(t)
	store := NewStore()
	sched := NewScheduler(store, []string{server.URL})
	t.Cleanup(func() { _ = sched.Stop() })

	require.NoError(t, sched.Start())
	require.Eventually(t, func() bool {
		return !sched.GetStatus().LastFetch.IsZero()
	}, 2*time.Second, 10*time.Millisecond)

	require.NoError(t, sched.ForceFetch(context.Background()))
}

func TestScheduler_GetStatus(t *testing.T) {
	urls := []string{iCalServer(t).URL}
	store := NewStore()
	sched := NewScheduler(store, urls)
	t.Cleanup(func() { _ = sched.Stop() })

	require.NoError(t, sched.Start())
	require.Eventually(t, func() bool {
		return sched.GetStatus().HolidayCount == 1
	}, 2*time.Second, 10*time.Millisecond)

	status := sched.GetStatus()
	assert.True(t, status.Running)
	assert.Equal(t, 1, status.URLCount)
	assert.Equal(t, 1, status.HolidayCount)
	require.NoError(t, status.LastError)
	assert.False(t, status.LastFetch.IsZero())
}

func TestScheduler_UpdateURLs(t *testing.T) {
	store := NewStore()
	sched := NewScheduler(store, []string{"http://a"})

	sched.UpdateURLs([]string{"http://b", "http://c"})
	assert.Equal(t, 2, sched.GetStatus().URLCount)
}

func TestScheduler_SetInterval(t *testing.T) {
	store := NewStore()
	sched := NewScheduler(store, nil)
	sched.SetInterval(123 * time.Millisecond)
	assert.Equal(t, 123*time.Millisecond, sched.interval)
}

func TestScheduler_FetchAndStoreImmediate(t *testing.T) {
	server := iCalServer(t)
	store := NewStore()
	sched := NewScheduler(store, []string{server.URL})

	holidays, err := sched.FetchAndStoreImmediate()
	require.NoError(t, err)
	assert.Len(t, holidays, 1)
	assert.Equal(t, 1, store.GetCount())
}

func TestScheduler_GetFetchLog_BeforeAnyFetch(t *testing.T) {
	store := NewStore()
	sched := NewScheduler(store, nil)
	assert.Contains(t, sched.GetFetchLog(), "No fetches performed yet")
}

func TestScheduler_GetFetchLog_AfterSuccess(t *testing.T) {
	server := iCalServer(t)
	store := NewStore()
	sched := NewScheduler(store, []string{server.URL})
	t.Cleanup(func() { _ = sched.Stop() })

	require.NoError(t, sched.Start())
	require.Eventually(t, func() bool {
		return !sched.GetStatus().LastFetch.IsZero()
	}, 2*time.Second, 10*time.Millisecond)

	log := sched.GetFetchLog()
	assert.Contains(t, log, "Success")
	assert.Contains(t, log, "Holidays: 1")
}

func TestScheduler_GetFetchLog_AfterError(t *testing.T) {
	store := NewStore()
	sched := NewScheduler(store, []string{errorServer(t, http.StatusNotFound).URL})
	t.Cleanup(func() { _ = sched.Stop() })

	require.NoError(t, sched.Start())
	require.Eventually(t, func() bool {
		return sched.GetStatus().LastError != nil
	}, 2*time.Second, 10*time.Millisecond)

	log := sched.GetFetchLog()
	assert.Contains(t, log, "Error:")
}

// stubFetcher counts calls without performing real I/O. Used inside a synctest bubble where
// HTTP I/O would not be durably blocking.
type stubFetcher struct {
	holidays []Holiday
	err      error
	calls    atomic.Int32
}

func (f *stubFetcher) FetchMultiple(_ context.Context, _ []string) ([]Holiday, error) {
	f.calls.Add(1)
	return f.holidays, f.err
}

func TestScheduler_PeriodicFetchFires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fetcher := &stubFetcher{holidays: []Holiday{{Date: "2026-06-15", Name: "Test"}}}
		sched := &Scheduler{
			store:    NewStore(),
			fetcher:  fetcher,
			urls:     []string{"http://unused"},
			stopChan: make(chan struct{}),
			interval: time.Millisecond,
		}

		require.NoError(t, sched.Start())

		// Initial goroutine should run, then the periodic ticker fires.
		synctest.Wait()
		assert.GreaterOrEqual(t, fetcher.calls.Load(), int32(1))

		// Advance fake time so the ticker fires again, then wait for it.
		time.Sleep(5 * time.Millisecond)
		synctest.Wait()
		assert.GreaterOrEqual(t, fetcher.calls.Load(), int32(2),
			"periodic ticker should have triggered additional fetches")

		require.NoError(t, sched.Stop())
	})
}

func TestLoadHolidayURLsFromEnv(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		t.Setenv("HOLIDAY_URLS", "")
		assert.Empty(t, LoadHolidayURLsFromEnv())
	})

	t.Run("single", func(t *testing.T) {
		t.Setenv("HOLIDAY_URLS", "http://example.com/cal.ics")
		assert.Equal(t, []string{"http://example.com/cal.ics"}, LoadHolidayURLsFromEnv())
	})

	t.Run("multiple with whitespace", func(t *testing.T) {
		t.Setenv("HOLIDAY_URLS", "http://a.example.com, http://b.example.com ,http://c.example.com")
		assert.Equal(t, []string{
			"http://a.example.com",
			"http://b.example.com",
			"http://c.example.com",
		}, LoadHolidayURLsFromEnv())
	})

	t.Run("skips empty entries", func(t *testing.T) {
		t.Setenv("HOLIDAY_URLS", "http://a.example.com,,,http://b.example.com,")
		assert.Equal(t, []string{
			"http://a.example.com",
			"http://b.example.com",
		}, LoadHolidayURLsFromEnv())
	})
}

func TestValidateHolidayURL(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		require.Error(t, ValidateHolidayURL(""))
		require.Error(t, ValidateHolidayURL("   "))
	})

	t.Run("missing scheme", func(t *testing.T) {
		assert.Error(t, ValidateHolidayURL("example.com/cal.ics"))
	})

	t.Run("non-http scheme", func(t *testing.T) {
		assert.Error(t, ValidateHolidayURL("ftp://example.com/cal.ics"))
	})

	t.Run("http valid", func(t *testing.T) {
		assert.NoError(t, ValidateHolidayURL("http://example.com/cal.ics"))
	})

	t.Run("https valid", func(t *testing.T) {
		assert.NoError(t, ValidateHolidayURL("https://example.com/cal.ics"))
	})
}
