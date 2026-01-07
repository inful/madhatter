package database

import (
	"database/sql"
	"fmt"

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

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
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
        leave_id TEXT,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (member_id) REFERENCES team_members(id),
        FOREIGN KEY (leave_id) REFERENCES leave_records(id)
    );

    CREATE TABLE IF NOT EXISTS calendar_subscriptions (
        id TEXT PRIMARY KEY,
        member_id TEXT NOT NULL,
        token TEXT UNIQUE NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (member_id) REFERENCES team_members(id)
    );
    `

	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}

	return &DB{db}, nil
}

func (db *DB) AddTeamMember(name, email string) (string, error) {
	if name == "" || email == "" {
		return "", fmt.Errorf("name and email cannot be empty")
	}
	id := uuid.New().String()
	query := `INSERT INTO team_members (id, name, email) VALUES (?, ?, ?)`
	_, err := db.Exec(query, id, name, email)
	return id, err
}

func (db *DB) GetActiveTeamMembers() ([]TeamMember, error) {
	rows, err := db.Query(`SELECT id, name, email, is_active, created_at FROM team_members WHERE is_active = 1 ORDER BY name`)
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
	row := db.QueryRow(`SELECT id, name, email, is_active, created_at FROM team_members WHERE email = ?`, email)

	var m TeamMember
	if err := row.Scan(&m.ID, &m.Name, &m.Email, &m.IsActive, &m.CreatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}
