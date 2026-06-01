package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
)

func setupAuth(db *database.DB, development bool) (*auth.AuthManager, *auth.Middleware, *auth.SessionManager, error) {
	if development {
		return setupDevelopmentAuth(db)
	}
	return setupProductionAuth(db)
}

//nolint:cyclop // Development setup wires resolver/lister closures with explicit control flow.
func setupDevelopmentAuth(db *database.DB) (*auth.AuthManager, *auth.Middleware, *auth.SessionManager, error) {
	log.Println("Development mode: Using fake OAuth provider")

	fakeConfig := auth.ProviderConfig{
		ClientID:     "dev-client-id",
		ClientSecret: "dev-client-secret", // #nosec G101
		RedirectURL:  "http://localhost:8080/auth/callback?provider=fake",
		AuthURL:      "/auth/fake/login",
		TokenURL:     "/auth/fake/token",
		UserInfoURL:  "/auth/fake/userinfo",
		Scope:        "read:user",
	}

	encryptor, err := auth.NewTokenEncryptor()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create token encryptor: %w", err)
	}

	providerFactory := auth.NewProviderFactory(map[string]auth.ProviderConfig{
		"fake": fakeConfig,
	})
	userService := auth.NewUserService(db.GetQueries(), encryptor)
	sessionManager := auth.NewSessionManager(db.GetQueries(), auth.SessionExpiryDuration)

	authManager := auth.NewAuthManager(providerFactory, userService, sessionManager)
	authMiddleware := auth.NewMiddleware(sessionManager)

	queries := db.GetQueries()
	fakeResolver := func(ctx context.Context, key string) (*auth.UserInfo, error) {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			return nil, errors.New("selected user key is required")
		}

		user, err := queries.GetUserByEmail(ctx, key)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}

			member, memberErr := queries.GetMemberByEmail(ctx, normalizedKey)
			if memberErr != nil {
				return nil, memberErr
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
		if user.IsActive.Valid && user.IsActive.Int64 == 0 {
			return nil, fmt.Errorf("user %q is inactive", normalizedKey)
		}

		return &auth.UserInfo{
			ID:       user.ID,
			Email:    user.Email,
			Name:     user.Name,
			Username: user.Name,
		}, nil
	}

	fakeUserLister := func(ctx context.Context) ([]auth.DevelopmentLoginUser, error) {
		byEmail := make(map[string]auth.DevelopmentLoginUser)

		users, err := queries.ListActiveUsers(ctx)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				users = nil
			} else {
				return nil, err
			}
		}

		for i := range users {
			byEmail[users[i].Email] = auth.DevelopmentLoginUser{
				Key:     users[i].Email,
				Name:    users[i].Name,
				Email:   users[i].Email,
				IsAdmin: auth.IsAdmin(users[i].IsAdmin),
			}
		}

		members, err := queries.GetActiveTeamMembers(ctx)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			members = nil
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

		devUsers := make([]auth.DevelopmentLoginUser, 0, len(byEmail))
		for _, user := range byEmail {
			devUsers = append(devUsers, user)
		}

		sort.Slice(devUsers, func(i, j int) bool {
			if devUsers[i].Name == devUsers[j].Name {
				return devUsers[i].Email < devUsers[j].Email
			}
			return devUsers[i].Name < devUsers[j].Name
		})

		return devUsers, nil
	}

	fakeProvider := auth.NewFakeProviderWithUserStore(fakeConfig, fakeResolver, fakeUserLister)
	authManager.RegisterProvider(fakeProvider)

	return authManager, authMiddleware, sessionManager, nil
}

func setupProductionAuth(db *database.DB) (*auth.AuthManager, *auth.Middleware, *auth.SessionManager, error) {
	authConfig := auth.LoadConfigFromEnv()

	if err := authConfig.Validate(); err != nil {
		log.Printf("WARNING: Authentication disabled - %v\n", err)
		log.Printf("The server will start without authentication. Features requiring login will be unavailable.\n")
		log.Printf("To enable authentication, configure OAuth provider environment variables.\n")
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
			log.Printf("Failed to create auth provider %q: %v\n", providerName, err)
			continue
		}
		authManager.RegisterProvider(provider)
	}

	return authManager, authMiddleware, sessionManager, nil
}
