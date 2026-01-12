package api

import (
	"fmt"
	"log"
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

func setupDevelopmentAuth(db *database.DB) (*auth.AuthManager, *auth.Middleware, *auth.SessionManager, error) {
	log.Println("Development mode: Using fake OAuth provider")

	fakeConfig := auth.ProviderConfig{
		ClientID:     "dev-client-id",
		ClientSecret: "dev-client-secret",
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

	fakeProvider := auth.NewFakeProvider(fakeConfig)
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
