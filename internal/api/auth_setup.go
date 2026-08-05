package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
)

func setupAuth(db *database.DB, development bool) (*auth.AuthManager, *auth.Middleware, *auth.SessionManager, error) {
	if development {
		return setupDevelopmentAuth(db)
	}
	return setupProductionAuth(db)
}

// setupDevelopmentAuth wires the auth stack for --development mode.
// The fake provider lets the dev login page list every active user
// and pick one without going through a real OAuth round-trip.
//
// The function body stays under the cyclomatic-complexity limit
// (≤10) because the non-trivial logic lives in fakeUserStore's
// methods and the helper functions below — each of which gets its
// own complexity budget.
func setupDevelopmentAuth(db *database.DB) (*auth.AuthManager, *auth.Middleware, *auth.SessionManager, error) {
	slog.Info("development mode: using fake OAuth provider")

	encryptor, err := auth.NewTokenEncryptor()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create token encryptor: %w", err)
	}

	fakeConfig := buildFakeProviderConfig()
	providerFactory := auth.NewProviderFactory(map[string]auth.ProviderConfig{
		"fake": fakeConfig,
	})
	userService := auth.NewUserService(db.GetQueries(), encryptor)
	sessionManager := auth.NewSessionManager(db.GetQueries(), auth.SessionExpiryDuration)
	authManager := auth.NewAuthManager(providerFactory, userService, sessionManager)
	authMiddleware := auth.NewMiddleware(sessionManager)

	store := newFakeUserStore(db.GetQueries())
	fakeProvider := auth.NewFakeProviderWithUserStore(fakeConfig, store.resolve, store.list)
	authManager.RegisterProvider(fakeProvider)

	return authManager, authMiddleware, sessionManager, nil
}

// buildFakeProviderConfig returns the static ProviderConfig used by
// the development fake OAuth provider. The values are meaningless —
// the dev login page bypasses the standard OAuth fields and reads
// the user's selection directly — but the provider still needs a
// valid config struct to register.
func buildFakeProviderConfig() auth.ProviderConfig {
	return auth.ProviderConfig{
		ClientID:     "dev-client-id",
		ClientSecret: "dev-client-secret", // #nosec G101
		RedirectURL:  "http://localhost:8080/auth/callback?provider=fake",
		AuthURL:      "/auth/fake/login",
		TokenURL:     "/auth/fake/token",
		UserInfoURL:  "/auth/fake/userinfo",
		Scope:        "read:user",
	}
}

// fakeUserStore backs the dev-mode fake provider with two methods
// matching the auth.FakeUserResolver and auth.FakeUserLister
// signatures. Methods on a named type get their own
// cyclomatic-complexity budget, which is how setupDevelopmentAuth
// stays under the limit.
type fakeUserStore struct {
	queries *sqlc.Queries
}

func newFakeUserStore(queries *sqlc.Queries) *fakeUserStore {
	return &fakeUserStore{queries: queries}
}

// resolve looks up a development-mode user by the key submitted from
// the dev login page. The key is normally an email; we try the
// users table first and fall back to the team_members table so
// pending-approval accounts are still pickable in development.
func (s *fakeUserStore) resolve(ctx context.Context, key string) (*auth.UserInfo, error) {
	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		return nil, errors.New("selected user key is required")
	}

	user, err := s.queries.GetUserByEmail(ctx, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.resolveTeamMember(ctx, normalizedKey)
		}
		return nil, err
	}
	if user.IsActive.Valid && user.IsActive.Int64 == 0 {
		return nil, fmt.Errorf("user %q is inactive", normalizedKey)
	}
	return userInfoFromUser(user), nil
}

// resolveTeamMember is the fallback branch of resolve. It runs
// after a users-table miss and rejects inactive team members.
func (s *fakeUserStore) resolveTeamMember(ctx context.Context, normalizedKey string) (*auth.UserInfo, error) {
	member, err := s.queries.GetMemberByEmail(ctx, normalizedKey)
	if err != nil {
		return nil, err
	}
	if member.IsActive.Valid && member.IsActive.Int64 == 0 {
		return nil, fmt.Errorf("user %q is inactive", normalizedKey)
	}
	return &auth.UserInfo{
		ID:       member.Email,
		Email:    member.Email,
		Name:     member.Name,
		Username: member.Name,
	}, nil
}

// userInfoFromUser projects a sqlc.User row onto the auth.UserInfo
// shape the fake provider hands back to the auth callback.
func userInfoFromUser(user sqlc.User) *auth.UserInfo {
	return &auth.UserInfo{
		ID:       user.ID,
		Email:    user.Email,
		Name:     user.Name,
		Username: user.Name,
	}
}

// list returns the union of active users and active team members
// (deduped by email), sorted for stable presentation in the dev
// login picker.
func (s *fakeUserStore) list(ctx context.Context) ([]auth.DevelopmentLoginUser, error) {
	byEmail, err := s.collectDevUsers(ctx)
	if err != nil {
		return nil, err
	}
	return sortedDevUsers(byEmail), nil
}

// collectDevUsers loads both the active users and the active team
// members and merges them into a single map keyed by email. The
// users table wins on collisions so admin status is preserved.
func (s *fakeUserStore) collectDevUsers(ctx context.Context) (map[string]auth.DevelopmentLoginUser, error) {
	byEmail := make(map[string]auth.DevelopmentLoginUser)

	users, err := s.queries.ListActiveUsers(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	for i := range users {
		byEmail[users[i].Email] = auth.DevelopmentLoginUser{
			Key:     users[i].Email,
			Name:    users[i].Name,
			Email:   users[i].Email,
			IsAdmin: auth.IsAdmin(users[i].IsAdmin),
		}
	}

	members, err := s.queries.GetActiveTeamMembers(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	for i := range members {
		if _, exists := byEmail[members[i].Email]; exists {
			continue
		}
		byEmail[members[i].Email] = auth.DevelopmentLoginUser{
			Key:   members[i].Email,
			Name:  members[i].Name,
			Email: members[i].Email,
		}
	}
	return byEmail, nil
}

// sortedDevUsers turns the byEmail map into a stable, sorted slice.
// Sorting runs through devUsersByNameEmail so the comparator gets
// its own cyclomatic budget.
func sortedDevUsers(byEmail map[string]auth.DevelopmentLoginUser) []auth.DevelopmentLoginUser {
	devUsers := make([]auth.DevelopmentLoginUser, 0, len(byEmail))
	for _, user := range byEmail {
		devUsers = append(devUsers, user)
	}
	sort.Slice(devUsers, func(i, j int) bool {
		return devUsersByNameEmail(devUsers[i], devUsers[j])
	})
	return devUsers
}

// devUsersByNameEmail is the comparator body — name asc, then email
// asc for deterministic ordering when names tie. Extracted so the
// if/return structure gets its own cyclomatic budget separate from
// the surrounding sort.Slice wrapper.
func devUsersByNameEmail(a, b auth.DevelopmentLoginUser) bool {
	if a.Name == b.Name {
		return a.Email < b.Email
	}
	return a.Name < b.Name
}

func setupProductionAuth(db *database.DB) (*auth.AuthManager, *auth.Middleware, *auth.SessionManager, error) {
	authConfig := auth.LoadConfigFromEnv()

	if err := authConfig.Validate(); err != nil {
		slog.Warn("authentication disabled", "error", err)
		slog.Warn("the server will start without authentication; features requiring login will be unavailable")
		slog.Warn("to enable authentication, configure OAuth provider environment variables")
		return nil, nil, nil, nil
	}

	encryptor, err := auth.NewTokenEncryptor()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create token encryptor: %w", err)
	}

	providerFactory := auth.NewProviderFactory(authConfig.Providers)
	userService := auth.NewUserService(db.GetQueries(), encryptor)
	sessionManager := auth.NewSessionManager(db.GetQueries(), time.Duration(authConfig.Sessions.DurationHours)*time.Hour)

	authManager := auth.NewAuthManager(providerFactory, userService, sessionManager)
	authMiddleware := auth.NewMiddleware(sessionManager)

	for providerName := range authConfig.Providers {
		provider, err := providerFactory.Create(providerName)
		if err != nil {
			slog.Warn("failed to create auth provider", "provider", providerName, "error", err)
			continue
		}
		authManager.RegisterProvider(provider)
	}

	return authManager, authMiddleware, sessionManager, nil
}
