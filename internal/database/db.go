package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
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

	return &DB{db}, nil
}

func (db *DB) AddTeamMember(name, email string) (string, error) {
	if name == "" || email == "" {
		return "", errors.New("name and email cannot be empty")
	}
	ctx := context.Background()
	id := uuid.New().String()
	query := `INSERT INTO team_members (id, name, email) VALUES (?, ?, ?)`
	_, err := db.ExecContext(ctx, query, id, name, email)
	return id, err
}

func (db *DB) GetActiveTeamMembers() ([]TeamMember, error) {
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, `SELECT id, name, email, is_active, created_at FROM team_members WHERE is_active = 1 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var members []TeamMember
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.ID, &m.Name, &m.Email, &m.IsActive, &m.CreatedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

func (db *DB) GetMemberByEmail(email string) (*TeamMember, error) {
	ctx := context.Background()
	row := db.QueryRowContext(ctx, `SELECT id, name, email, is_active, created_at FROM team_members WHERE email = ?`, email)

	var m TeamMember
	if err := row.Scan(&m.ID, &m.Name, &m.Email, &m.IsActive, &m.CreatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

// CreateCalendarSubscription creates a new calendar subscription for a team member.
func (db *DB) CreateCalendarSubscription(memberID string) (string, error) {
	ctx := context.Background()
	token := uuid.New().String()
	id := uuid.New().String()
	query := `INSERT INTO calendar_subscriptions (id, member_id, token) VALUES (?, ?, ?)`
	_, err := db.ExecContext(ctx, query, id, memberID, token)
	return token, err
}

func (db *DB) GetMemberByToken(token string) (*TeamMember, error) {
	ctx := context.Background()
	query := `SELECT tm.id, tm.name, tm.email, tm.is_active, tm.created_at
              FROM calendar_subscriptions cs
              JOIN team_members tm ON cs.member_id = tm.id
              WHERE cs.token = ?`
	row := db.QueryRowContext(ctx, query, token)

	var m TeamMember
	if err := row.Scan(&m.ID, &m.Name, &m.Email, &m.IsActive, &m.CreatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

func (db *DB) GetUpcomingAssignments(memberID string, days int) ([]RotaAssignment, error) {
	ctx := context.Background()
	query := `SELECT id, date, member_id, is_cover, original_assignment_id
              FROM rota_assignments
              WHERE member_id = ? AND date >= date('now') AND date <= date('now', '+'||?||' days')
              ORDER BY date`
	rows, err := db.QueryContext(ctx, query, memberID, days)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var assignments []RotaAssignment
	for rows.Next() {
		var a RotaAssignment
		var originalAssignmentID sql.NullString
		err := rows.Scan(&a.ID, &a.Date, &a.MemberID, &a.IsCover, &originalAssignmentID)
		if err != nil {
			return nil, err
		}
		if originalAssignmentID.Valid {
			a.OriginalAssignmentID = &originalAssignmentID.String
		}
		assignments = append(assignments, a)
	}
	return assignments, nil
}
