package database

import (
	"context"
	"database/sql"
	"errors"
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

func (db *DB) GetActiveTeamMembers(ctx context.Context) ([]TeamMember, error) {
	members, err := db.queries.GetActiveTeamMembers(ctx)
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

func (db *DB) GetMemberByEmail(ctx context.Context, email string) (*TeamMember, error) {
	member, err := db.queries.GetMemberByEmail(ctx, email)
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

func (db *DB) GetMemberByID(ctx context.Context, id string) (*TeamMember, error) {
	member, err := db.queries.GetMemberByID(ctx, id)
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

	return &TeamMember{
		ID:        member.ID,
		Name:      member.Name,
		Email:     member.Email,
		IsActive:  member.IsActive.Valid && member.IsActive.Int64 == 1,
		CreatedAt: member.CreatedAt.Time,
	}, nil
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
			CreatedAt:            time.Now(),
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
