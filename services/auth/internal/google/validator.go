package google

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/api/idtoken"
)

var (
	ErrInvalidToken       = errors.New("invalid or expired Google ID token")
	ErrMissingSubject     = errors.New("google token missing subject (sub) claim")
	ErrMissingEmail       = errors.New("google token missing email claim")
	ErrEmailNotVerified   = errors.New("google account email is not verified")
	ErrValidatorNotConfig = errors.New("google oauth client ID is not configured")
)

// TokenPayload contains verified identity claims extracted from a valid Google ID token.
type TokenPayload struct {
	Subject       string // Google immutable unique user ID (sub)
	Email         string
	EmailVerified bool
	Name          string
}

// TokenValidator defines the contract for verifying Google ID tokens.
type TokenValidator interface {
	Validate(ctx context.Context, rawIDToken string) (*TokenPayload, error)
}

// IDTokenValidator uses Google's official Go idtoken package to verify tokens.
type IDTokenValidator struct {
	clientID string
}

// NewIDTokenValidator creates a new Google ID token validator for the configured audience (client ID).
func NewIDTokenValidator(clientID string) *IDTokenValidator {
	return &IDTokenValidator{clientID: clientID}
}

// Validate verifies the token signature, audience, issuer, expiry, and essential claims.
func (v *IDTokenValidator) Validate(ctx context.Context, rawIDToken string) (*TokenPayload, error) {
	if v.clientID == "" {
		return nil, ErrValidatorNotConfig
	}
	if rawIDToken == "" {
		return nil, ErrInvalidToken
	}

	payload, err := idtoken.Validate(ctx, rawIDToken, v.clientID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	if payload.Subject == "" {
		return nil, ErrMissingSubject
	}

	email, _ := payload.Claims["email"].(string)
	if email == "" {
		return nil, ErrMissingEmail
	}

	emailVerified, _ := payload.Claims["email_verified"].(bool)
	if !emailVerified {
		return nil, ErrEmailNotVerified
	}

	name, _ := payload.Claims["name"].(string)

	return &TokenPayload{
		Subject:       payload.Subject,
		Email:         email,
		EmailVerified: emailVerified,
		Name:          name,
	}, nil
}
