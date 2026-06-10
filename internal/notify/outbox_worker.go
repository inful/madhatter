package notify

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/notify/channels"
)

// MaxBackoff is the upper bound for retry delays. The exponential
// growth from BackoffBase stops here, regardless of attempts.
const MaxBackoff = time.Hour

// drainBatchSize is how many rows the worker claims per tick. Tuned
// to be small enough that a single slow SMTP send doesn't starve
// other channels, and large enough that a tick handles meaningful
// traffic.
const drainBatchSize = 32

// channelResolver returns the Channel registered under the given name.
// The production resolver is a static map; tests can inject a custom
// resolver that swaps the channel implementation on demand.
type channelResolver interface {
	Resolve(name string) (channels.Channel, bool)
}

// staticChannelResolver is a fixed map of channels keyed by name.
type staticChannelResolver struct {
	channels map[string]channels.Channel
}

func (r staticChannelResolver) Resolve(name string) (channels.Channel, bool) {
	ch, ok := r.channels[name]
	return ch, ok
}

// Worker drains the notification_outbox table. It is the production
// counterpart to LogNotifier: LogNotifier calls channels directly,
// while Worker writes to the outbox and dispatches them later.
type Worker struct {
	db       *database.DB
	resolver channelResolver
	cfg      OutboxConfig
	logger   *slog.Logger
}

// NewWorker returns a Worker that uses the given DB, channel resolver,
// and outbox configuration. A nil logger falls back to slog.Default().
func NewWorker(db *database.DB, resolver channelResolver, cfg OutboxConfig, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		db:       db,
		resolver: resolver,
		cfg:      cfg,
		logger:   logger,
	}
}

// Run blocks until ctx is cancelled, polling the outbox at the
// configured interval and dispatching due rows to the registered
// channel. Each row that fails is rescheduled with exponential
// backoff up to MaxBackoff; rows that hit cfg.MaxAttempts are
// marked dead.
//
// Run is intended to be launched in a goroutine by the server at
// startup. Cancellation via ctx is the only shutdown signal.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	w.logger.Info("notify worker started",
		slog.Duration("poll_interval", w.cfg.PollInterval),
		slog.Int("max_attempts", w.cfg.MaxAttempts),
	)

	for {
		// Drain eagerly on entry, then on every tick. This way a
		// short poll interval still sees traffic quickly without
		// waiting a full interval after Start.
		w.drain(ctx)

		select {
		case <-ctx.Done():
			w.logger.Info("notify worker stopped")
			return
		case <-ticker.C:
		}
	}
}

// drain processes one batch of due outbox rows. It is exposed (rather
// than being a private helper) so tests can drive a single drain
// deterministically without depending on the ticker.
func (w *Worker) drain(ctx context.Context) {
	rows, err := w.db.ClaimDueOutboxEntries(ctx, drainBatchSize)
	if err != nil {
		w.logger.Warn("notify worker: claim due entries failed",
			slog.String("err", err.Error()))
		return
	}
	for i := range rows {
		// Check cancellation between rows so shutdown is prompt.
		if ctx.Err() != nil {
			return
		}
		w.dispatch(ctx, rows[i])
	}
}

// dispatch handles a single outbox row: look up the channel, call
// Send, and update the row's status/attempts accordingly.
func (w *Worker) dispatch(ctx context.Context, row database.OutboxEntry) {
	ch, ok := w.resolver.Resolve(row.Channel)
	if !ok {
		// Unknown channel; mark dead immediately. This shouldn't
		// happen in production because the notifier only writes rows
		// for channels it knows about, but defensive coding costs
		// little.
		_ = w.db.MarkOutboxDead(ctx, row.ID, "unknown channel: "+row.Channel, time.Now())
		w.logger.Warn("notify worker: unknown channel",
			slog.String("id", row.ID),
			slog.String("channel", row.Channel),
		)
		return
	}

	msg := channels.OutboundMessage{
		EventKind:      row.EventKind,
		Recipient:      row.Recipient,
		RecipientName:  row.RecipientName,
		Subject:        row.Subject,
		Body:           row.Body,
		UnsubscribeURL: row.UnsubscribeURL,
	}
	err := ch.Send(ctx, msg)
	if err == nil {
		if markErr := w.db.MarkOutboxSent(ctx, row.ID); markErr != nil {
			w.logger.Warn("notify worker: mark sent failed",
				slog.String("id", row.ID),
				slog.String("err", markErr.Error()))
		}
		return
	}

	// Transient failure: increment attempts and reschedule with
	// exponential backoff. If attempts has hit the max, mark dead.
	attempts := row.Attempts + 1
	if attempts >= w.cfg.MaxAttempts {
		_ = w.db.MarkOutboxDead(ctx, row.ID, err.Error(), time.Now())
		w.logger.Warn("notify worker: max attempts reached, marking dead",
			slog.String("id", row.ID),
			slog.String("channel", row.Channel),
			slog.Int("attempts", attempts),
			slog.String("err", err.Error()),
		)
		return
	}
	next := w.computeNextAttempt(attempts)
	if markErr := w.db.MarkOutboxFailed(ctx, row.ID, err.Error(), next); markErr != nil {
		w.logger.Warn("notify worker: mark failed",
			slog.String("id", row.ID),
			slog.String("err", markErr.Error()))
	}
	w.logger.Info("notify worker: retry scheduled",
		slog.String("id", row.ID),
		slog.String("channel", row.Channel),
		slog.Int("attempts", attempts),
		slog.Duration("next_in", time.Until(next)),
		slog.String("err", err.Error()),
	)
}

// computeNextAttempt returns the backoff for the (1-indexed) attempt
// number. The formula is base * 2^(attempts-1), capped at MaxBackoff.
func (w *Worker) computeNextAttempt(attempts int) time.Time {
	if attempts < 1 {
		attempts = 1
	}
	const backoffExponentBase = 2
	mult := math.Pow(backoffExponentBase, float64(attempts-1))
	d := time.Duration(float64(w.cfg.BackoffBase) * mult)
	d = min(d, MaxBackoff)
	return time.Now().Add(d)
}
