package wfh

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

const defaultSettlementInterval = 24 * time.Hour

// Scheduler runs the WFH settlement job on a configurable interval.
type Scheduler struct {
	service  *Service
	interval time.Duration
	stopChan chan struct{}
	wg       sync.WaitGroup
	running  bool
	mu       sync.Mutex
}

// NewScheduler creates a new WFH scheduler with a daily interval.
func NewScheduler(service *Service) *Scheduler {
	return &Scheduler{
		service:  service,
		interval: defaultSettlementInterval,
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
	s.running = true
	s.mu.Unlock()

	log.Println("Starting WFH settlement scheduler")

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

	log.Println("Stopping WFH settlement scheduler")
	close(s.stopChan)
	s.wg.Wait()
}

func (s *Scheduler) runSettle(ctx context.Context) {
	if err := s.service.SettlePendingRequests(ctx); err != nil {
		log.Printf("WFH settlement error: %v\n", err)
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
