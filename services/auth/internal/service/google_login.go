package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	platformerrors "tradedrift/platform/errors"
	"tradedrift/services/auth/internal/repository"
)

var usernameSanitizeRegex = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// GoogleLogin handles ID token verification, existing identity lookup,
// safe account-linking detection, or atomic new user creation.
func (s *Service) GoogleLogin(ctx context.Context, idToken string) (*UserDTO, *TokenPairDTO, *LinkRequiredDTO, error) {
	if s.googleValidator == nil {
		s.log.Error("Google token validator is not configured")
		return nil, nil, nil, platformerrors.New(platformerrors.CodeInternal, "google authentication is temporarily unavailable")
	}

	// 1. Verify Google ID token (never log raw token)
	payload, err := s.googleValidator.Validate(ctx, idToken)
	if err != nil {
		s.log.Warn("Google login token validation failed", zap.Error(err))
		return nil, nil, nil, platformerrors.New(platformerrors.CodeInvalidCredentials, "invalid or expired Google token")
	}

	// Normalize email to prevent casing mismatch issues
	payload.Email = normalizeEmail(payload.Email)

	// 2. PRIMARY CHECK: Look up by Google sub first (identity already linked)
	identity, err := s.identityRepo.GetByProviderAndUID(ctx, "google", payload.Subject)
	if err != nil {
		s.log.Error("Failed to query user auth identity by sub", zap.Error(err))
		return nil, nil, nil, err
	}

	if identity != nil {
		u, err := s.userRepo.GetByID(ctx, identity.UserID)
		if err != nil {
			s.log.Error("Failed to query user by linked ID", zap.String("userID", identity.UserID), zap.Error(err))
			return nil, nil, nil, err
		}
		if u == nil {
			s.log.Error("Linked user record not found in users table", zap.String("userID", identity.UserID))
			return nil, nil, nil, platformerrors.New(platformerrors.CodeNotFound, "user account not found")
		}

		userDTO, tokens, err := s.handleExistingUserSession(ctx, u)
		if err != nil {
			return nil, nil, nil, err
		}
		return userDTO, tokens, nil, nil
	}

	// 3. SECONDARY CHECK: No Google link, but does email exist in users table?
	existingUser, err := s.userRepo.GetByIdentifier(ctx, payload.Email)
	if err != nil {
		s.log.Error("Failed to query user by email", zap.Error(err))
		return nil, nil, nil, err
	}

	if existingUser != nil {
		return s.initiateLinkRequired(ctx, payload.Subject, payload.Email, existingUser)
	}

	// 4. BRAND NEW USER: Atomically create user + identity mapping + outbox event in PostgreSQL
	now := time.Now().UTC()
	var newUser *repository.User
	var identityRecord *repository.UserAuthIdentity
	var lastCreateErr error

	for attempt := 0; attempt < 3; attempt++ {
		userID := uuid.NewString()
		username := s.deriveUniqueUsername(ctx, payload.Name, payload.Email, attempt)

		newUser = &repository.User{
			ID:                  userID,
			Email:               payload.Email,
			Username:            username,
			PasswordHash:        "", // Stored as NULL in PostgreSQL
			TokenVersion:        1,
			Role:                "user",
			Status:              "VERIFIED",
			FailedLoginAttempts: 0,
			EmailVerifiedAt:     &now,
			CreatedAt:           now,
			UpdatedAt:           now,
		}

		identityRecord = &repository.UserAuthIdentity{
			ID:             uuid.NewString(),
			UserID:         userID,
			Provider:       "google",
			ProviderUserID: payload.Subject,
			Email:          payload.Email,
			CreatedAt:      now,
			UpdatedAt:      now,
		}

		eventID := uuid.NewString()
		eventPayload, _ := json.Marshal(map[string]string{
			"user_id":  userID,
			"email":    payload.Email,
			"username": username,
		})

		err := s.identityRepo.CreateUserWithIdentity(ctx, newUser, identityRecord, eventID, eventPayload)
		if err == nil {
			lastCreateErr = nil
			break
		}
		lastCreateErr = err

		// Handle race conditions safely:
		// A. Did another concurrent request create this Google identity?
		existingIdent, checkErr := s.identityRepo.GetByProviderAndUID(ctx, "google", payload.Subject)
		if checkErr == nil && existingIdent != nil {
			u, fetchErr := s.userRepo.GetByID(ctx, existingIdent.UserID)
			if fetchErr == nil && u != nil {
				userDTO, tokens, sessionErr := s.handleExistingUserSession(ctx, u)
				if sessionErr != nil {
					return nil, nil, nil, sessionErr
				}
				return userDTO, tokens, nil, nil
			}
		}

		// B. Did another concurrent request register this email?
		existingByEmail, emailErr := s.userRepo.GetByIdentifier(ctx, payload.Email)
		if emailErr == nil && existingByEmail != nil {
			return s.initiateLinkRequired(ctx, payload.Subject, payload.Email, existingByEmail)
		}

		// C. If neither sub nor email conflicted, this was a username collision — loop and retry with new username suffix
		s.log.Warn("Retrying user creation after unique constraint collision", zap.Int("attempt", attempt), zap.Error(err))
	}

	if lastCreateErr != nil {
		s.log.Error("Failed to atomically create new user after conflict retries", zap.Error(lastCreateErr))
		return nil, nil, nil, lastCreateErr
	}

	// 5. Initialize starter wallet (10,000 USDT mock balance) with durable retry
	var walletErr error
	for attempt := 1; attempt <= 3; attempt++ {
		walletCtx, walletCancel := context.WithTimeout(ctx, 4*time.Second)
		walletErr = s.walletCl.InitializeWallet(walletCtx, newUser.ID)
		walletCancel()
		if walletErr == nil {
			break
		}
		s.log.Warn("Retrying wallet initialization for new Google user",
			zap.String("userID", newUser.ID),
			zap.Int("attempt", attempt),
			zap.Error(walletErr),
		)
		time.Sleep(100 * time.Millisecond)
	}

	if walletErr != nil {
		s.log.Error("Critical: Failed to initialize starter wallet for new Google user",
			zap.String("userID", newUser.ID),
			zap.Error(walletErr),
		)
		return nil, nil, nil, platformerrors.Wrap(walletErr, platformerrors.CodeInternal, "account created, but starter wallet initialization failed. Please retry sign-in to complete initialization.")
	}

	// 6. Issue session tokens
	tokens, err := s.issueTokenPair(ctx, newUser.ID, payload.Email, "user", 1)
	if err != nil {
		s.log.Error("Failed to issue session tokens for new Google user", zap.String("userID", newUser.ID), zap.Error(err))
		return nil, nil, nil, err
	}

	s.log.Info("Successfully registered and authenticated new Google user",
		zap.String("userID", newUser.ID),
	)

	return &UserDTO{
		ID:       newUser.ID,
		Email:    payload.Email,
		Username: newUser.Username,
	}, tokens, nil, nil
}

// handleExistingUserSession centralizes account-status validation, wallet readiness checks,
// and session token issuance for all returning Google logins.
func (s *Service) handleExistingUserSession(ctx context.Context, u *repository.User) (*UserDTO, *TokenPairDTO, error) {
	now := time.Now().UTC()

	// Enforce account status restrictions (reject pending, suspended or banned)
	if u.Status == "PENDING_VERIFICATION" {
		s.log.Warn("Google login rejected: account pending verification", zap.String("userID", u.ID))
		return nil, nil, platformerrors.New(platformerrors.CodeAccountNotActive, "account is not verified. Please verify your email first.")
	}
	if u.Status == "SUSPENDED" || u.Status == "BANNED" {
		s.log.Warn("Google login rejected: account suspended or banned", zap.String("userID", u.ID), zap.String("status", u.Status))
		return nil, nil, platformerrors.New(platformerrors.CodePermissionDenied, fmt.Sprintf("your account has been %s", u.Status))
	}

	// Durable wallet readiness check: ensure starter wallet is initialized (fail-closed)
	if err := s.walletCl.InitializeWallet(ctx, u.ID); err != nil {
		s.log.Error("Failed to verify/initialize wallet readiness for Google user", zap.String("userID", u.ID), zap.Error(err))
		return nil, nil, platformerrors.Wrap(err, platformerrors.CodeInternal, "wallet verification failed. Please try logging in again.")
	}

	// Issue session tokens
	tokens, err := s.issueTokenPair(ctx, u.ID, u.Email, u.Role, u.TokenVersion)
	if err != nil {
		s.log.Error("Failed to issue tokens for Google login", zap.String("userID", u.ID), zap.Error(err))
		return nil, nil, err
	}

	_ = s.userRepo.ResetFailedLogin(ctx, u.ID, now)

	return &UserDTO{
		ID:       u.ID,
		Email:    u.Email,
		Username: u.Username,
	}, tokens, nil
}

// initiateLinkRequired creates a short-lived link token and returns LINK_REQUIRED DTO
func (s *Service) initiateLinkRequired(ctx context.Context, sub, email string, existingUser *repository.User) (*UserDTO, *TokenPairDTO, *LinkRequiredDTO, error) {
	// Enforce account restrictions before offering linking
	if existingUser.Status == "PENDING_VERIFICATION" {
		s.log.Warn("Google login rejected: local account pending verification", zap.String("userID", existingUser.ID))
		return nil, nil, nil, platformerrors.New(platformerrors.CodeAccountNotActive, "an account with this email exists but is pending email verification. Please verify your email first before linking.")
	}
	if existingUser.Status == "SUSPENDED" || existingUser.Status == "BANNED" {
		return nil, nil, nil, platformerrors.New(platformerrors.CodePermissionDenied, fmt.Sprintf("your account has been %s", existingUser.Status))
	}

	// Check if account has no password (e.g., created via another OAuth provider)
	if existingUser.PasswordHash == "" {
		return nil, nil, nil, platformerrors.New(platformerrors.CodeFailedPrecondition, "an account with this email exists without a password. Please sign in directly.")
	}

	// Safe policy: Do NOT automatically merge. Require password verification.
	linkToken := uuid.NewString()
	linkData := map[string]string{
		"sub":     sub,
		"email":   email,
		"user_id": existingUser.ID,
	}
	dataBytes, _ := json.Marshal(linkData)

	// Store in Redis with 5-minute TTL
	redisKey := "auth:google_link:" + linkToken
	if err := s.rdb.Set(ctx, redisKey, string(dataBytes), 5*time.Minute).Err(); err != nil {
		s.log.Error("Failed to store Google link token in Redis", zap.Error(err))
		return nil, nil, nil, platformerrors.New(platformerrors.CodeInternal, "failed to initiate account linking")
	}

	s.log.Info("Google login requires password confirmation to link account",
		zap.String("userID", existingUser.ID),
	)

	return nil, nil, &LinkRequiredDTO{
		LinkToken: linkToken,
		Email:     email,
		Message:   "An account with this email already exists. Please verify your password to securely link your Google account.",
	}, nil
}

// deriveUniqueUsername generates a valid, alphanumeric username between 3 and 32 characters.
func (s *Service) deriveUniqueUsername(ctx context.Context, name, email string, attempt int) string {
	base := strings.TrimSpace(name)
	if base == "" {
		parts := strings.Split(email, "@")
		base = parts[0]
	}

	// Remove non-alphanumeric chars
	sanitized := usernameSanitizeRegex.ReplaceAllString(base, "")
	if len(sanitized) < 3 {
		sanitized = "trader_" + sanitized
	}
	if len(sanitized) > 20 {
		sanitized = sanitized[:20]
	}

	// Add random suffix to avoid collision
	suffix := uuid.NewString()[:6]
	if attempt > 0 {
		suffix = fmt.Sprintf("%x_%s", attempt, uuid.NewString()[:5])
	}

	candidate := fmt.Sprintf("%s_%s", sanitized, suffix)
	if len(candidate) > 32 {
		candidate = candidate[:32]
	}

	return candidate
}
