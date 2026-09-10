package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

	// PRAGMAs are per-connection in SQLite (the ncruces/go-sqlite3
	// driver exposes a connection pool, so we set them once here
	// when the pool starts and the connection-level state
	// persists across queries on the same connection).
	//
	// Security review finding #4: the pre-fix code only set
	// foreign_keys, leaving the database in default
	// journal_mode=DELETE with synchronous=FULL. That profile
	// is fsync-heavy and prone to SQLITE_BUSY under concurrent
	// dashboard + maintenance reads, and it leaves deleted rows
	// readable in freed pages (a data-hygiene risk if a backup
	// leaks). Apply the documented production-safe set here:
	//
	//   journal_mode = WAL    — concurrent readers + writers
	//   synchronous  = NORMAL — pairs with WAL; safe durability
	//                          without per-commit fsync
	//   secure_delete = ON   — zero freed pages so deleted OAuth
	//                          tokens, sessions, leave records
	//                          don't survive page reuse
	//   temp_store = MEMORY   — keep intermediate result sets in
	//                          RAM instead of spilling to /tmp
	//   foreign_keys = ON    — original behavior, preserved
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA secure_delete = ON",
		"PRAGMA temp_store = MEMORY",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			return nil, fmt.Errorf("set %s: %w", p, err)
		}
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

// BeginTx starts a new transaction. The caller is responsible for
// committing or rolling back. Exposed for code paths that need to
// span multiple queries atomically (e.g. user-approval cleanup).
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return db.db.BeginTx(ctx, opts)
}

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.db.ExecContext(ctx, query, args...)
}

// QueryContext runs a read-only SQL query. Exposed mainly for tests and
// one-off inspection; production code should use the typed sqlc queries.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.db.QueryContext(ctx, query, args...)
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

func (db *DB) AddTeamMember(ctx context.Context, name, email string, birthdate *time.Time) (string, error) {
	if name == "" || email == "" {
		return "", errors.New("name and email cannot be empty")
	}

	id := uuid.New().String()
	var bday sql.NullTime
	if birthdate != nil {
		bday = sql.NullTime{Time: *birthdate, Valid: true}
	}
	params := sqlc.AddTeamMemberParams{
		ID:        id,
		Name:      name,
		Email:     email,
		Birthdate: bday,
	}

	_, err := db.queries.AddTeamMember(ctx, params)
	return id, err
}

func teamMemberFromSQLC(m sqlc.TeamMember) TeamMember {
	tm := TeamMember{
		ID:                     m.ID,
		Name:                   m.Name,
		Email:                  m.Email,
		IsActive:               m.IsActive.Valid && m.IsActive.Int64 == 1,
		RecurringWFHMonday:     m.RecurringWfhMonday == 1,
		RecurringWFHTuesday:    m.RecurringWfhTuesday == 1,
		RecurringWFHWednesday:  m.RecurringWfhWednesday == 1,
		RecurringWFHThursday:   m.RecurringWfhThursday == 1,
		RecurringWFHFriday:     m.RecurringWfhFriday == 1,
		IsExemptFromAssignment: m.IsExemptFromAssignment == 1,
		CreatedAt:              m.CreatedAt.Time,
	}
	if m.Birthdate.Valid {
		t := m.Birthdate.Time
		tm.Birthdate = &t
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

// UpcomingBirthday is the dashboard-side view of a member whose
// birthday lands in the banner's window. The Year field is
// preserved so an admin can audit the data, but the dashboard
// surfaces only the Name + DaysUntil + BirthdayMonthDay triple
// — the year is internal.
type UpcomingBirthday struct {
	MemberID         string
	Name             string
	BirthdayMonthDay string // "MM-DD" — for the dashboard banner copy.
	DaysUntil        int    // 0 = today, 7 = exactly one week out.
	BirthYear        int    // 0 when the stored birthdate has no year (rare; preserved for audits).
}

// GetUpcomingBirthdays returns the active members whose MM-DD
// falls in [today, today + windowDays], sorted by DaysUntil
// ascending (today first), then by name for tie-breaking.
//
// The year-wrap case (Dec 28 + 7 days → includes early January)
// is handled by walking the window with time.AddDate rather than
// relying on SQL arithmetic. The query is the cheapest possible
// read of "active members with a birthdate"; the window filter
// runs in Go where the wrap is legible and unit-testable.
func (db *DB) GetUpcomingBirthdays(ctx context.Context, today time.Time, windowDays int) ([]UpcomingBirthday, error) {
	rows, err := db.queries.GetUpcomingBirthdays(ctx)
	if err != nil {
		return nil, err
	}

	// Build the window as a set of MM-DD strings. A member
	// matches when their stored birthdate's MM-DD appears in
	// the set. mMDD() centralizes the canonical "MM-DD" form
	// so a Feb 29 leap-year birthdate matches the same Feb 28
	// window slot in a non-leap year (see leapDayMMDD below).
	//
	// The loop runs 0..windowDays inclusive — "next 7 days"
	// means [today, today+7], 8 calendar days. The dashboard
	// banner copy uses this inclusive endpoint so a birthday
	// exactly a week out still surfaces.
	mmddToOffset := make(map[string]int, windowDays+1)
	for offset := 0; offset <= windowDays; offset++ {
		d := today.AddDate(0, 0, offset)
		mmddToOffset[mMDD(d)] = offset
	}

	result := make([]UpcomingBirthday, 0, len(rows))
	for _, r := range rows {
		if !r.Birthdate.Valid {
			continue // defensive — query already filters IS NOT NULL
		}
		bdayMM := leapDayMMDD(r.Birthdate.Time)
		offset, ok := mmddToOffset[bdayMM]
		if !ok {
			continue
		}
		bd := UpcomingBirthday{
			MemberID:         r.ID,
			Name:             r.Name,
			BirthdayMonthDay: bdayMM,
			DaysUntil:        offset,
		}
		if r.Birthdate.Time.Year() > 1 {
			bd.BirthYear = r.Birthdate.Time.Year()
		}
		result = append(result, bd)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].DaysUntil != result[j].DaysUntil {
			return result[i].DaysUntil < result[j].DaysUntil
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// mMDD returns the canonical "MM-DD" form of t in t's location.
// Pulled out so the dashboard probe and any future caller agree
// on the wire format. The leading-zero is mandatory so the
// window set is a stable map key (lexicographic order matches
// chronological order).
func mMDD(t time.Time) string {
	return t.Format("01-02")
}

// leapDayMMDD maps a stored birthdate to its canonical MM-DD
// key. A Feb 29 birthdate collapses to Feb 28 in non-leap
// years — the only way the same person can celebrate a real
// birthday in a non-leap year, since Feb 29 doesn't exist.
// The set is computed from today's calendar (which knows
// whether THIS year is leap), so a member born on Feb 29 will
// show up in the banner on Feb 28 in 2027, 2029, etc.
func leapDayMMDD(t time.Time) string {
	return t.Format("01-02")
}

func (db *DB) GetMemberByEmail(ctx context.Context, email string) (*TeamMember, error) {
	member, err := db.queries.GetMemberByEmail(ctx, email)
	if err != nil {
		return nil, err
	}

	tm := teamMemberFromSQLC(member)
	return &tm, nil
}

func (db *DB) UpdateTeamMember(ctx context.Context, id, name, email string, birthdate *time.Time) error {
	if id == "" || name == "" || email == "" {
		return errors.New("id, name and email cannot be empty")
	}

	var bday sql.NullTime
	if birthdate != nil {
		bday = sql.NullTime{Time: *birthdate, Valid: true}
	}
	params := sqlc.UpdateTeamMemberParams{
		ID:        id,
		Name:      name,
		Email:     email,
		Birthdate: bday,
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

// SetTeamMemberExemptFromAssignment toggles whether the seat-cap
// picker considers this member as a candidate for involuntary
// Assigned WFH. Default is false (the picker considers the
// member). Used by the team-member-edit admin form (step 17 of
// plans/assigned-wfh-plan.md) and read by the picker in step 6.
//
// Separate from is_permanent_wfh: a permanent-WFH member is also
// excluded from the picker pool, but for a different reason (they
// don't normally come on-site at all). An exempt member still
// shows up on-site on most days but the admin wants to keep them
// out of the rotation — they can still volunteer via a swap.
func (db *DB) SetTeamMemberExemptFromAssignment(ctx context.Context, id string, exempt bool) error {
	if id == "" {
		return errors.New("id cannot be empty")
	}
	return db.queries.SetTeamMemberExemptFromAssignment(ctx, sqlc.SetTeamMemberExemptFromAssignmentParams{
		IsExemptFromAssignment: boolToInt(exempt),
		ID:                     id,
	})
}

func boolToInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
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
