package database

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
	_ "modernc.org/sqlite"
)

type DB struct {
	queries *sqlc.Queries
	db      *sql.DB
}

func New(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	// Enable foreign keys
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return nil, err
	}

	// Create tables
	schema := `
    CREATE TABLE IF NOT EXISTS team_members (
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL,
        email TEXT UNIQUE NOT NULL,
        is_active INTEGER DEFAULT 1,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );

    CREATE TABLE IF NOT EXISTS leave_records (
        id TEXT PRIMARY KEY,
        member_id TEXT NOT NULL,
        start_date DATE NOT NULL,
        end_date DATE NOT NULL,
        type TEXT NOT NULL,
        cover_member_id TEXT,
        status TEXT NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (member_id) REFERENCES team_members(id),
        FOREIGN KEY (cover_member_id) REFERENCES team_members(id)
    );

    CREATE TABLE IF NOT EXISTS rota_assignments (
        id TEXT PRIMARY KEY,
        date DATE NOT NULL,
        member_id TEXT NOT NULL,
        is_cover INTEGER DEFAULT 0,
        original_assignment_id TEXT,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (member_id) REFERENCES team_members(id),
        FOREIGN KEY (original_assignment_id) REFERENCES rota_assignments(id)
    );

    CREATE TABLE IF NOT EXISTS calendar_subscriptions (
        id TEXT PRIMARY KEY,
        member_id TEXT NOT NULL,
        token TEXT UNIQUE NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (member_id) REFERENCES team_members(id)
    );
    `

	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, err
	}

	queries := sqlc.New(db)
	return &DB{queries: queries, db: db}, nil
}

func (db *DB) Close() error {
	return db.db.Close()
}

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.db.ExecContext(ctx, query, args...)
}

func (db *DB) AddTeamMember(name, email string) (string, error) {
	if name == "" || email == "" {
		return "", errors.New("name and email cannot be empty")
	}

	id := uuid.New().String()
	params := sqlc.AddTeamMemberParams{
		ID:    id,
		Name:  name,
		Email: email,
	}

	_, err := db.queries.AddTeamMember(context.Background(), params)
	return id, err
}

func (db *DB) GetActiveTeamMembers() ([]TeamMember, error) {
	members, err := db.queries.GetActiveTeamMembers(context.Background())
	if err != nil {
		return nil, err
	}

	result := make([]TeamMember, len(members))
	for i, m := range members {
		result[i] = TeamMember{
			ID:        m.ID,
			Name:      m.Name,
			Email:     m.Email,
			IsActive:  m.IsActive.Valid && m.IsActive.Int64 == 1,
			CreatedAt: m.CreatedAt.Time,
		}
	}
	return result, nil
}

func (db *DB) GetMemberByEmail(email string) (*TeamMember, error) {
	member, err := db.queries.GetMemberByEmail(context.Background(), email)
	if err != nil {
		return nil, err
	}

	return &TeamMember{
		ID:        member.ID,
		Name:      member.Name,
		Email:     member.Email,
		IsActive:  member.IsActive.Valid && member.IsActive.Int64 == 1,
		CreatedAt: member.CreatedAt.Time,
	}, nil
}

func (db *DB) CreateCalendarSubscription(memberID string) (string, error) {
	// Verify member exists
	_, err := db.queries.GetMemberByID(context.Background(), memberID)
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

	_, err = db.queries.CreateCalendarSubscription(context.Background(), params)
	return token, err
}

func (db *DB) GetMemberByToken(token string) (*TeamMember, error) {
	member, err := db.queries.GetMemberByToken(context.Background(), token)
	if err != nil {
		return nil, err
	}

	return &TeamMember{
		ID:        member.ID,
		Name:      member.Name,
		Email:     member.Email,
		IsActive:  member.IsActive.Valid && member.IsActive.Int64 == 1,
		CreatedAt: member.CreatedAt.Time,
	}, nil
}

func (db *DB) GetUpcomingAssignments(memberID string, days int) ([]RotaAssignment, error) {
	params := sqlc.GetUpcomingAssignmentsParams{
		MemberID: memberID,
		Column2:  sql.NullString{String: strconv.Itoa(days), Valid: true},
	}

	assignments, err := db.queries.GetUpcomingAssignments(context.Background(), params)
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
			CreatedAt:            time.Now(),
		}
	}
	return result, nil
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
