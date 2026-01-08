package auth

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

// UserService handles user-related operations.
type UserService struct {
	db *sqlc.Queries
}

// NewUserService creates a new user service.
func NewUserService(db *sqlc.Queries) *UserService {
	return &UserService{db: db}
}

// GetOrCreateUser finds a user by provider info or creates a new one.
func (us *UserService) GetOrCreateUser(ctx context.Context, userInfo *UserInfo, providerName string) (*sqlc.User, error) {
	// Try to find existing user by provider
	existingUser, err := us.db.GetUserByProvider(ctx, sqlc.GetUserByProviderParams{
		Provider:   providerName,
		ProviderID: userInfo.ID,
	})
	if err == nil {
		return &existingUser, nil
	}

	// If not found, check if user exists by email
	existingUserByEmail, err := us.db.GetUserByEmail(ctx, userInfo.Email)
	if err == nil {
		// User exists but different provider - update it
		updateErr := us.db.UpdateUser(ctx, sqlc.UpdateUserParams{
			ID:       existingUserByEmail.ID,
			Name:     userInfo.Name,
			IsAdmin:  existingUserByEmail.IsAdmin,
			IsActive: existingUserByEmail.IsActive,
		})
		if updateErr != nil {
			return nil, fmt.Errorf("failed to update user: %w", updateErr)
		}
		return &existingUserByEmail, nil
	}

	// Create new user
	userID := uuid.New().String()

	// Check if this is the first user - make them admin
	allUsers, _ := us.db.ListActiveUsers(ctx)
	isAdmin := len(allUsers) == 0

	newUser, err := us.db.CreateUser(ctx, sqlc.CreateUserParams{
		ID:         userID,
		Email:      userInfo.Email,
		Name:       userInfo.Name,
		Provider:   providerName,
		ProviderID: userInfo.ID,
		IsAdmin:    sql.NullInt64{Int64: boolToInt(isAdmin), Valid: true},
		IsActive:   sql.NullInt64{Int64: 1, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return &newUser, nil
}

// StoreOAuthToken stores OAuth tokens for a user.
func (us *UserService) StoreOAuthToken(ctx context.Context, userID, provider string, token *sqlc.OauthToken) error {
	// Check if token exists
	_, err := us.db.GetOAuthToken(ctx, sqlc.GetOAuthTokenParams{
		UserID:   userID,
		Provider: provider,
	})

	if err == nil {
		// Update existing token
		return us.db.UpdateOAuthToken(ctx, sqlc.UpdateOAuthTokenParams{
			UserID:       userID,
			Provider:     provider,
			AccessToken:  token.AccessToken,
			RefreshToken: token.RefreshToken,
			TokenType:    token.TokenType,
			ExpiresAt:    token.ExpiresAt,
		})
	}

	// Create new token
	_, err = us.db.CreateOAuthToken(ctx, sqlc.CreateOAuthTokenParams{
		ID:           uuid.New().String(),
		UserID:       userID,
		Provider:     provider,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresAt:    token.ExpiresAt,
	})
	return err
}

// GetOAuthToken retrieves OAuth tokens for a user.
func (us *UserService) GetOAuthToken(ctx context.Context, userID, provider string) (*sqlc.OauthToken, error) {
	token, err := us.db.GetOAuthToken(ctx, sqlc.GetOAuthTokenParams{
		UserID:   userID,
		Provider: provider,
	})
	if err != nil {
		return nil, err
	}
	return &token, nil
}

// IsAdmin checks if a user is an admin.
func (us *UserService) IsAdmin(ctx context.Context, userID string) (bool, error) {
	user, err := us.db.GetUserByID(ctx, userID)
	if err != nil {
		return false, err
	}
	return user.IsAdmin.Valid && user.IsAdmin.Int64 == 1, nil
}

// boolToInt converts bool to int for SQL storage.
func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
