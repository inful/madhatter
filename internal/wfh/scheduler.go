package wfh

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Scheduler runs the WFH settlement job on a configurable interval.
type Scheduler struct {
	service  *Service
	interval time.Duration
	stopChan chan struct{}
	wg       sync.WaitGroup
	running  bool
	mu       sync.Mutex
	// ticks counts the number of times runSettle has completed, for
	// tests that need to distinguish the immediate tick from a periodic
	// one. Bumped in runSettle's defer so both success and error paths
	// are counted. Safe to read concurrently via atomic.Int64.
	ticks atomic.Int64
}

// NewScheduler creates a new WFH scheduler. The tick interval comes
// from the service config (WFH_SETTLEMENT_INTERVAL env var, default
// 15 minutes). The old constant of 24h was too coarse for an
// interactive UI — a 15-minute tick keeps the perceived settlement
// latency under the typical request workflow.
func NewScheduler(service *Service) *Scheduler {
	interval := defaultSettlementInterval
	if service != nil && service.Config().SettlementInterval > 0 {
		interval = service.Config().SettlementInterval
	}
	return &Scheduler{
		service:  service,
		interval: interval,
		stopChan: make(chan struct{}),
	}
}

// Start begins the background settlement scheduler.
// Settlement is also run immediately on start.
func (s *Scheduler) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("WFH scheduler is already running")
	}
	s.stopChan = make(chan struct{})
	s.running = true
	s.mu.Unlock()

	slog.Info("starting WFH settlement scheduler")

	// Run once immediately.
	s.wg.Go(func() {
		s.runSettle(context.Background())
	})

	// Run periodically.
	s.wg.Go(func() {
		s.periodicSettle()
	})

	return nil
}

// Stop gracefully stops the scheduler.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	slog.Info("stopping WFH settlement scheduler")
	close(s.stopChan)
	s.wg.Wait()
}

func (s *Scheduler) runSettle(ctx context.Context) {
	defer s.ticks.Add(1)
	if err := s.service.SettlePendingRequests(ctx); err != nil {
		slog.Error("WFH settlement error", "error", err)
	}
	// Purge runs after settle so a fresh deploy self-heals into the
	// current+previous retention policy on the first tick. Purge
	// failures are logged, never fatal — a broken purge must not block
	// settlement, and the next tick will retry.
	if s.service.IsPurgeEnabled() {
		if _, _, err := s.service.PurgePastPeriods(ctx); err != nil {
			slog.Error("WFH past-period purge error", "error", err)
		}
	}
}

func (s *Scheduler) periodicSettle() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.runSettle(context.Background())
		case <-s.stopChan:
			return
		}
	}
}
