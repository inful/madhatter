package auth

import "errors"

var (
	ErrProviderNotFound = errors.New("auth: provider not found")
	ErrInvalidSession   = errors.New("auth: invalid or expired session")
	ErrUnauthorized     = errors.New("auth: unauthorized access")
	ErrInvalidState     = errors.New("auth: invalid state parameter")
	ErrTokenExchange    = errors.New("auth: token exchange failed")
	ErrUserInfo         = errors.New("auth: failed to retrieve user info")
)
