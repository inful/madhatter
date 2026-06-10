package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

type DB struct {
	queries *sqlc.Queries
	db      *sql.DB
	// holidayChecker, if set, reports whether a given UTC date (year/month/day
	// at midnight UTC) falls on a holiday. Used by feature layers to reject
	// state that would be meaningless on non-working days.
	holidayChecker func(time.Time) bool
}

// HolidayChecker is the function signature for checking whether a date is a holiday.
type HolidayChecker func(time.Time) bool

// SetHolidayChecker installs a holiday checker used by features that should
// refuse to operate on holidays (e.g. WFH requests). Pass nil to disable.
func (db *DB) SetHolidayChecker(checker HolidayChecker) {
	db.holidayChecker = checker
}

// IsHoliday reports whether the given date falls on a holiday according to the
// installed checker. Returns false if no checker is installed.
func (db *DB) IsHoliday(date time.Time) bool {
	if db.holidayChecker == nil {
		return false
	}
	return db.holidayChecker(date)
}

func New(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	// Enable foreign keys
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return nil, err
	}

	// Run database migrations
	if err := RunMigrations(db); err != nil {
		return nil, err
	}

	queries := sqlc.New(db)
	return &DB{queries: queries, db: db}, nil
}

func (db *DB) Close() error {
	return db.db.Close()
}

// GetQueries returns the underlying sqlc.Queries instance.
// This is needed for auth components that require direct SQLC access.
func (db *DB) GetQueries() *sqlc.Queries {
	return db.queries
}

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.db.ExecContext(ctx, query, args...)
}

// CreateBackup creates a consistent SQLite database snapshot and returns its bytes.
func (db *DB) CreateBackup(ctx context.Context) ([]byte, error) {
	backupPath := filepath.Join(os.TempDir(), fmt.Sprintf("madhatter-backup-%d.db", time.Now().UnixNano()))
	defer func() {
		_ = os.Remove(backupPath)
	}()

	if _, err := db.db.ExecContext(ctx, "VACUUM INTO ?", backupPath); err != nil {
		return nil, err
	}

	//nolint:gosec // backupPath is generated internally with a fixed prefix under os.TempDir.
	backupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, err
	}

	return backupBytes, nil
}

func (db *DB) AddTeamMember(ctx context.Context, name, email string) (string, error) {
	if name == "" || email == "" {
		return "", errors.New("name and email cannot be empty")
	}

	id := uuid.New().String()
	params := sqlc.AddTeamMemberParams{
		ID:    id,
		Name:  name,
		Email: email,
	}

	_, err := db.queries.AddTeamMember(ctx, params)
	return id, err
}

func teamMemberFromSQLC(m sqlc.TeamMember) TeamMember {
	tm := TeamMember{
		ID:                    m.ID,
		Name:                  m.Name,
		Email:                 m.Email,
		IsActive:              m.IsActive.Valid && m.IsActive.Int64 == 1,
		RecurringWFHMonday:    m.RecurringWfhMonday == 1,
		RecurringWFHTuesday:   m.RecurringWfhTuesday == 1,
		RecurringWFHWednesday: m.RecurringWfhWednesday == 1,
		RecurringWFHThursday:  m.RecurringWfhThursday == 1,
		RecurringWFHFriday:    m.RecurringWfhFriday == 1,
		CreatedAt:             m.CreatedAt.Time,
	}
	tm.IsPermanentWFH = tm.HasPermanentRecurringWFH()
	return tm
}

func (db *DB) GetActiveTeamMembers(ctx context.Context) ([]TeamMember, error) {
	members, err := db.queries.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]TeamMember, len(members))
	for i := range members {
		result[i] = teamMemberFromSQLC(members[i])
	}
	return result, nil
}

func (db *DB) GetMemberByEmail(ctx context.Context, email string) (*TeamMember, error) {
	member, err := db.queries.GetMemberByEmail(ctx, email)
	if err != nil {
		return nil, err
	}

	tm := teamMemberFromSQLC(member)
	return &tm, nil
}

func (db *DB) UpdateTeamMember(ctx context.Context, id, name, email string) error {
	if id == "" || name == "" || email == "" {
		return errors.New("id, name and email cannot be empty")
	}

	params := sqlc.UpdateTeamMemberParams{
		ID:    id,
		Name:  name,
		Email: email,
	}

	return db.queries.UpdateTeamMember(ctx, params)
}

func (db *DB) DeleteTeamMember(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("id cannot be empty")
	}

	return db.queries.DeleteTeamMember(ctx, id)
}

func (db *DB) SetTeamMemberPermanentWFH(ctx context.Context, id string, isPermanentWFH bool) error {
	days := RecurringWFHDays{}
	if isPermanentWFH {
		days = RecurringWFHDays{Monday: true, Tuesday: true, Wednesday: true, Thursday: true, Friday: true}
	}

	return db.SetTeamMemberRecurringWFHDays(ctx, id, days)
}

func (db *DB) SetTeamMemberRecurringWFHDays(ctx context.Context, id string, days RecurringWFHDays) error {
	if id == "" {
		return errors.New("id cannot be empty")
	}

	toInt := func(v bool) int64 {
		if v {
			return 1
		}
		return 0
	}

	return db.queries.SetTeamMemberRecurringWFHDays(ctx, sqlc.SetTeamMemberRecurringWFHDaysParams{
		RecurringWfhMonday:    toInt(days.Monday),
		RecurringWfhTuesday:   toInt(days.Tuesday),
		RecurringWfhWednesday: toInt(days.Wednesday),
		RecurringWfhThursday:  toInt(days.Thursday),
		RecurringWfhFriday:    toInt(days.Friday),
		ID:                    id,
	})
}

func (db *DB) GetMemberByID(ctx context.Context, id string) (*TeamMember, error) {
	member, err := db.queries.GetMemberByID(ctx, id)
	if err != nil {
		return nil, err
	}

	tm := teamMemberFromSQLC(member)
	return &tm, nil
}

func (db *DB) CreateCalendarSubscription(ctx context.Context, memberID string) (string, error) {
	// Verify member exists
	_, err := db.queries.GetMemberByID(ctx, memberID)
	if err != nil {
		return "", errors.New("member not found")
	}

	token := uuid.New().String()
	id := uuid.New().String()

	params := sqlc.CreateCalendarSubscriptionParams{
		ID:       id,
		MemberID: memberID,
		Token:    token,
	}

	_, err = db.queries.CreateCalendarSubscription(ctx, params)
	return token, err
}

func (db *DB) GetMemberByToken(ctx context.Context, token string) (*TeamMember, error) {
	member, err := db.queries.GetMemberByToken(ctx, token)
	if err != nil {
		return nil, err
	}

	tm := teamMemberFromSQLC(member)
	return &tm, nil
}

// TouchRotaSubscription records that the rota ICS calendar was fetched for the given token.
func (db *DB) TouchRotaSubscription(ctx context.Context, token string) error {
	return db.queries.TouchRotaSubscription(ctx, token)
}

// TouchMeetingsSubscription records that the meetings ICS calendar was fetched for the given token.
func (db *DB) TouchMeetingsSubscription(ctx context.Context, token string) error {
	return db.queries.TouchMeetingsSubscription(ctx, token)
}

// GetSubscriptionsByMemberID returns all calendar subscriptions for a given member.
func (db *DB) GetSubscriptionsByMemberID(ctx context.Context, memberID string) ([]CalendarSubscription, error) {
	rows, err := db.queries.GetSubscriptionsByMemberID(ctx, memberID)
	if err != nil {
		return nil, err
	}

	result := make([]CalendarSubscription, len(rows))
	for i := range rows {
		r := &rows[i]
		sub := CalendarSubscription{
			ID:       r.ID,
			MemberID: r.MemberID,
			Token:    r.Token,
		}
		if r.CreatedAt.Valid {
			sub.CreatedAt = r.CreatedAt.Time
		}
		if r.LastUsedAt.Valid {
			t := r.LastUsedAt.Time
			sub.LastUsedAt = &t
		}
		if r.LastUsedRotaAt.Valid {
			t := r.LastUsedRotaAt.Time
			sub.LastUsedRotaAt = &t
		}
		if r.LastUsedMeetingsAt.Valid {
			t := r.LastUsedMeetingsAt.Time
			sub.LastUsedMeetingsAt = &t
		}
		result[i] = sub
	}
	return result, nil
}

// GetAllSubscriptions returns all calendar subscriptions ordered by last_used_at ascending.
func (db *DB) GetAllSubscriptions(ctx context.Context) ([]CalendarSubscription, error) {
	rows, err := db.queries.GetAllSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]CalendarSubscription, len(rows))
	for i := range rows {
		r := &rows[i]
		sub := CalendarSubscription{
			ID:       r.ID,
			MemberID: r.MemberID,
			Token:    r.Token,
		}
		if r.CreatedAt.Valid {
			sub.CreatedAt = r.CreatedAt.Time
		}
		if r.LastUsedAt.Valid {
			t := r.LastUsedAt.Time
			sub.LastUsedAt = &t
		}
		if r.LastUsedRotaAt.Valid {
			t := r.LastUsedRotaAt.Time
			sub.LastUsedRotaAt = &t
		}
		if r.LastUsedMeetingsAt.Valid {
			t := r.LastUsedMeetingsAt.Time
			sub.LastUsedMeetingsAt = &t
		}
		result[i] = sub
	}
	return result, nil
}

// DeleteStaleSubscriptions deletes subscriptions that have not been used since the given cutoff time.
// Subscriptions that have never been used are considered stale if created before the cutoff.
func (db *DB) DeleteStaleSubscriptions(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoffNull := sql.NullTime{Time: cutoff, Valid: true}
	result, err := db.queries.DeleteStaleSubscriptions(ctx, sqlc.DeleteStaleSubscriptionsParams{
		LastUsedAt: cutoffNull,
		CreatedAt:  cutoffNull,
	})
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// MemberSubscriptionActivity records whether a member's subscriptions have been
// actively used for the rota and/or meetings calendars recently.
type MemberSubscriptionActivity struct {
	RotaActive     bool
	MeetingsActive bool
}

// GetSubscriptionActivityByMember returns a map of member ID → subscription
// activity for all members who have at least one subscription active since
// the given cutoff time.
func (db *DB) GetSubscriptionActivityByMember(ctx context.Context, since time.Time) (map[string]MemberSubscriptionActivity, error) {
	rows, err := db.queries.GetAllSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]MemberSubscriptionActivity)
	for i := range rows {
		r := &rows[i]
		act := result[r.MemberID]
		if r.LastUsedRotaAt.Valid && r.LastUsedRotaAt.Time.After(since) {
			act.RotaActive = true
		}
		if r.LastUsedMeetingsAt.Valid && r.LastUsedMeetingsAt.Time.After(since) {
			act.MeetingsActive = true
		}
		result[r.MemberID] = act
	}
	return result, nil
}

func (db *DB) GetUpcomingAssignments(ctx context.Context, memberID string, days int) ([]RotaAssignment, error) {
	params := sqlc.GetUpcomingAssignmentsParams{
		MemberID: memberID,
		Column2:  sql.NullString{String: strconv.Itoa(days), Valid: true},
	}

	assignments, err := db.queries.GetUpcomingAssignments(ctx, params)
	if err != nil {
		return nil, err
	}

	result := make([]RotaAssignment, len(assignments))
	for i, a := range assignments {
		result[i] = RotaAssignment{
			ID:                   a.ID,
			Date:                 a.Date.Format("2006-01-02"),
			MemberID:             a.MemberID,
			IsCover:              a.IsCover.Valid && a.IsCover.Int64 == 1,
			OriginalAssignmentID: getNullString(a.OriginalAssignmentID),
			CreatedAt:            time.Time{},
		}
	}
	return result, nil
}

// DeleteRotaAssignment deletes a rota assignment by ID.
func (db *DB) DeleteRotaAssignment(ctx context.Context, id string) error {
	return db.queries.DeleteRotaAssignment(ctx, id)
}

// Helper functions.
func getNullString(nullStr sql.NullString) *string {
	if nullStr.Valid {
		return &nullStr.String
	}
	return nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// CreateAPIToken creates a new API token for a user with optional expiration.
func (db *DB) CreateAPIToken(ctx context.Context, userID, name, tokenHash string, expiresAt sql.NullTime) (string, error) {
	id := uuid.New().String()
	params := sqlc.CreateAPITokenParams{
		ID:        id,
		UserID:    userID,
		Name:      name,
		TokenHash: tokenHash,
		IsActive:  sql.NullInt64{Int64: 1, Valid: true},
		ExpiresAt: expiresAt,
	}

	_, err := db.queries.CreateAPIToken(ctx, params)
	return id, err
}

// GetAPITokensByUser gets all API tokens for a user.
func (db *DB) GetAPITokensByUser(ctx context.Context, userID string) ([]sqlc.ApiToken, error) {
	return db.queries.GetAPITokensByUser(ctx, userID)
}

// GetAPITokenByID gets an API token by ID.
func (db *DB) GetAPITokenByID(ctx context.Context, tokenID string) (sqlc.ApiToken, error) {
	return db.queries.GetAPITokenByID(ctx, tokenID)
}

// GetAPITokenByHash gets an API token by its hash.
func (db *DB) GetAPITokenByHash(ctx context.Context, tokenHash string) (sqlc.ApiToken, error) {
	return db.queries.GetAPITokenByHash(ctx, tokenHash)
}

// UpdateAPITokenLastUsed updates the last used timestamp of an API token.
func (db *DB) UpdateAPITokenLastUsed(ctx context.Context, tokenID string) error {
	_, err := db.queries.UpdateAPITokenLastUsed(ctx, tokenID)
	return err
}

// DeleteAPIToken deletes an API token.
func (db *DB) DeleteAPIToken(ctx context.Context, tokenID string) error {
	_, err := db.queries.DeleteAPIToken(ctx, tokenID)
	return err
}

// DeactivateAPIToken deactivates an API token.
func (db *DB) DeactivateAPIToken(ctx context.Context, tokenID string) error {
	_, err := db.queries.DeactivateAPIToken(ctx, tokenID)
	return err
}

// CleanupExpiredTokens removes expired API tokens.
func (db *DB) CleanupExpiredTokens(ctx context.Context) error {
	_, err := db.queries.CleanupExpiredTokens(ctx)
	return err
}
