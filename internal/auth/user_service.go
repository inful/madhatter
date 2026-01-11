package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

// UserService handles user-related operations.
type UserService struct {
	db        *sqlc.Queries
	encryptor *TokenEncryptor
}

// NewUserService creates a new user service.
func NewUserService(db *sqlc.Queries, encryptor *TokenEncryptor) *UserService {
	return &UserService{
		db:        db,
		encryptor: encryptor,
	}
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
		// User exists with same email but different provider
		// Don't allow login with different provider - this could be a security issue
		return nil, fmt.Errorf("user with email %s already exists with provider %s, cannot login with %s",
			userInfo.Email, existingUserByEmail.Provider, providerName)
	}

	// Create new user
	userID := uuid.New().String()

	// Use atomic query that checks admin count and creates user in one operation
	newUser, err := us.db.CreateUserAsFirstAdmin(ctx, sqlc.CreateUserAsFirstAdminParams{
		ID:         userID,
		Email:      userInfo.Email,
		Name:       userInfo.Name,
		Provider:   providerName,
		ProviderID: userInfo.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return &newUser, nil
}

// EnsureTeamMember ensures a corresponding team member exists for an authenticated user.
//
// If the member exists, it will be reactivated (if inactive) and its name updated.
// If not, it will be created.
func (us *UserService) EnsureTeamMember(ctx context.Context, userInfo *UserInfo) error {
	name, email, err := normalizeUserIdentity(userInfo)
	if err != nil {
		return err
	}

	member, err := us.db.GetMemberByEmail(ctx, email)
	if err == nil {
		return us.syncTeamMember(ctx, member, name, email)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to look up team member: %w", err)
	}

	if err := us.insertTeamMember(ctx, name, email); err == nil {
		return nil
	} else if isUniqueConstraint(err) {
		member, fetchErr := us.db.GetMemberByEmail(ctx, email)
		if fetchErr != nil {
			return fmt.Errorf("failed to fetch team member after unique constraint: %w", fetchErr)
		}
		return us.syncTeamMember(ctx, member, name, email)
	} else {
		return fmt.Errorf("failed to create team member: %w", err)
	}
}

func normalizeUserIdentity(userInfo *UserInfo) (string, string, error) {
	if userInfo == nil {
		return "", "", errors.New("userInfo is required")
	}

	name := strings.TrimSpace(userInfo.Name)
	email := strings.TrimSpace(userInfo.Email)
	if name == "" || email == "" {
		return "", "", errors.New("userInfo name and email are required")
	}

	return name, email, nil
}

func (us *UserService) insertTeamMember(ctx context.Context, name, email string) error {
	_, err := us.db.AddTeamMember(ctx, sqlc.AddTeamMemberParams{ID: uuid.New().String(), Name: name, Email: email})
	return err
}

func (us *UserService) syncTeamMember(ctx context.Context, member sqlc.TeamMember, name, email string) error {
	if member.IsActive.Valid && member.IsActive.Int64 == 0 {
		if err := us.db.ActivateTeamMember(ctx, member.ID); err != nil {
			return fmt.Errorf("failed to activate team member: %w", err)
		}
	}
	if member.Name == name {
		return nil
	}
	if err := us.db.UpdateTeamMember(ctx, sqlc.UpdateTeamMemberParams{ID: member.ID, Name: name, Email: email}); err != nil {
		return fmt.Errorf("failed to update team member: %w", err)
	}
	return nil
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE")
}

// StoreOAuthToken stores OAuth tokens for a user.
func (us *UserService) StoreOAuthToken(ctx context.Context, userID, provider string, token *sqlc.OauthToken) error {
	// Encrypt tokens before storage
	encryptedAccessToken, err := us.encryptor.Encrypt(token.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt access token: %w", err)
	}

	var encryptedRefreshToken sql.NullString
	if token.RefreshToken.Valid && token.RefreshToken.String != "" {
		encryptedRefresh, encryptErr := us.encryptor.Encrypt(token.RefreshToken.String)
		if encryptErr != nil {
			return fmt.Errorf("failed to encrypt refresh token: %w", encryptErr)
		}
		encryptedRefreshToken = sql.NullString{String: encryptedRefresh, Valid: true}
	}

	// Check if token exists
	_, err = us.db.GetOAuthToken(ctx, sqlc.GetOAuthTokenParams{
		UserID:   userID,
		Provider: provider,
	})

	if err == nil {
		// Update existing token
		return us.db.UpdateOAuthToken(ctx, sqlc.UpdateOAuthTokenParams{
			UserID:       userID,
			Provider:     provider,
			AccessToken:  encryptedAccessToken,
			RefreshToken: encryptedRefreshToken,
			TokenType:    token.TokenType,
			ExpiresAt:    token.ExpiresAt,
		})
	}

	// Create new token
	_, err = us.db.CreateOAuthToken(ctx, sqlc.CreateOAuthTokenParams{
		ID:           uuid.New().String(),
		UserID:       userID,
		Provider:     provider,
		AccessToken:  encryptedAccessToken,
		RefreshToken: encryptedRefreshToken,
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

	// Decrypt tokens
	decryptedAccessToken, err := us.encryptor.Decrypt(token.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt access token: %w", err)
	}

	var decryptedRefreshToken sql.NullString
	if token.RefreshToken.Valid && token.RefreshToken.String != "" {
		decryptedRefresh, decryptErr := us.encryptor.Decrypt(token.RefreshToken.String)
		if decryptErr != nil {
			return nil, fmt.Errorf("failed to decrypt refresh token: %w", decryptErr)
		}
		decryptedRefreshToken = sql.NullString{String: decryptedRefresh, Valid: true}
	}

	// Return token with decrypted values
	token.AccessToken = decryptedAccessToken
	token.RefreshToken = decryptedRefreshToken

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
