package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

// OutboxEntry is the in-Go representation of a row in notification_outbox.
// It mirrors the SQLC-generated struct but uses plain Go types where possible
// so callers don't have to deal with sql.NullString / sql.NullTime.
type OutboxEntry struct {
	ID            string
	EventKind     string
	Channel       string
	Recipient     string
	RecipientName string
	Subject       string
	Body          string
	Attempts      int
	LastError     string
	NextAttemptAt time.Time
	Status        string
	CreatedAt     time.Time
	SentAt        *time.Time
}

// OutboxChannel is the set of supported channel identifiers written to
// notification_outbox.channel. Kept as named constants so callers don't
// scatter magic strings around.
const (
	OutboxChannelEmail = "email"
)

// OutboxStatus values used in notification_outbox.status.
const (
	OutboxStatusPending = "pending"
	OutboxStatusSent    = "sent"
	OutboxStatusDead    = "dead"
)

// EnqueueOutboxEntry writes a new outbox row and returns the generated ID.
// The caller supplies an explicit ID via uuid.New().String() if it needs to
// reference the row later; for the common fire-and-forget case, leave id
// empty and a fresh UUID is generated.
func (db *DB) EnqueueOutboxEntry(ctx context.Context, eventKind, channel, recipient, recipientName, subject, body string) (string, error) {
	id := uuid.New().String()

	var rn sql.NullString
	if recipientName != "" {
		rn = sql.NullString{String: recipientName, Valid: true}
	}

	if _, err := db.queries.CreateOutboxEntry(ctx, sqlc.CreateOutboxEntryParams{
		ID:            id,
		EventKind:     eventKind,
		Channel:       channel,
		Recipient:     recipient,
		RecipientName: rn,
		Subject:       subject,
		Body:          body,
	}); err != nil {
		return "", err
	}
	return id, nil
}

// ClaimDueOutboxEntries returns up to limit pending outbox rows whose
// next_attempt_at is in the past, oldest first. The caller is responsible
// for marking each row sent, failed, or dead after dispatch.
func (db *DB) ClaimDueOutboxEntries(ctx context.Context, limit int) ([]OutboxEntry, error) {
	rows, err := db.queries.ClaimDueOutboxEntries(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	out := make([]OutboxEntry, 0, len(rows))
	for i := range rows {
		out = append(out, outboxFromSQLC(rows[i]))
	}
	return out, nil
}

// MarkOutboxSent records that the row was successfully delivered.
func (db *DB) MarkOutboxSent(ctx context.Context, id string) error {
	_, err := db.queries.MarkOutboxSent(ctx, id)
	return err
}

// MarkOutboxFailed records a transient delivery failure and schedules the
// next retry at nextAttempt. It does not change status (the row stays
// 'pending' so the worker re-picks it up). The nextAttempt timestamp
// is normalized to UTC because SQLite compares against CURRENT_TIMESTAMP
// in UTC.
func (db *DB) MarkOutboxFailed(ctx context.Context, id, lastError string, nextAttempt time.Time) error {
	_, err := db.queries.MarkOutboxFailed(ctx, sqlc.MarkOutboxFailedParams{
		LastError:     sql.NullString{String: lastError, Valid: lastError != ""},
		NextAttemptAt: nextAttempt.UTC(),
		ID:            id,
	})
	return err
}

// MarkOutboxDead marks the row terminally failed. Used when retries are
// exhausted. The at timestamp is normalized to UTC.
func (db *DB) MarkOutboxDead(ctx context.Context, id, lastError string, at time.Time) error {
	_, err := db.queries.MarkOutboxDead(ctx, sqlc.MarkOutboxDeadParams{
		LastError:     sql.NullString{String: lastError, Valid: lastError != ""},
		NextAttemptAt: at.UTC(),
		ID:            id,
	})
	return err
}

// GetOutboxEntry fetches a single row by ID. Mainly for tests and admin
// inspection; production code rarely needs it.
func (db *DB) GetOutboxEntry(ctx context.Context, id string) (OutboxEntry, error) {
	r, err := db.queries.GetOutboxEntry(ctx, id)
	if err != nil {
		return OutboxEntry{}, err
	}
	return outboxFromSQLC(r), nil
}

// ListOutboxIDs returns up to limit outbox row IDs ordered by
// created_at. Intended for tests; production code should use the typed
// queries.
func (db *DB) ListOutboxIDs(ctx context.Context, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT id FROM notification_outbox ORDER BY created_at LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// QueryOutboxRowsForTest is a test-only helper that returns the first
// up-to-limit outbox row IDs. Used by notify package tests to find
// a row to inspect after a manual drain.
func (db *DB) QueryOutboxRowsForTest(ctx context.Context, limit int) ([]string, error) {
	return db.ListOutboxIDs(ctx, limit)
}

func outboxFromSQLC(r sqlc.NotificationOutbox) OutboxEntry {
	e := OutboxEntry{
		ID:            r.ID,
		EventKind:     r.EventKind,
		Channel:       r.Channel,
		Recipient:     r.Recipient,
		RecipientName: r.RecipientName.String,
		Subject:       r.Subject,
		Body:          r.Body,
		Attempts:      int(r.Attempts),
		LastError:     r.LastError.String,
		NextAttemptAt: r.NextAttemptAt.UTC(),
		Status:        r.Status,
		CreatedAt:     r.CreatedAt.UTC(),
	}
	if r.SentAt.Valid {
		t := r.SentAt.Time.UTC()
		e.SentAt = &t
	}
	return e
}
