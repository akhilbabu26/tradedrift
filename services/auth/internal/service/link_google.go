package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	platformerrors "tradedrift/platform/errors"
	"tradedrift/services/auth/internal/repository"
)

// compareAndDeleteScript ensures a client only deletes the lock if it still holds it.
var compareAndDeleteScript = redis.NewScript(`
	if redis.call("get", KEYS[1]) == ARGV[1] then
		return redis.call("del", KEYS[1])
	else
		return 0
	end
`)

// consumeLinkTokenScript atomically validates the token state and transitions it to CONSUMED with 60s TTL.
// Return codes:
//  1: Successfully consumed
// -1: Token not found / expired
// -2: Token already consumed (replay detected)
var consumeLinkTokenScript = redis.NewScript(`
	local val = redis.call("get", KEYS[1])
	if not val then
		return -1
	end
	local data = cjson.decode(val)
	if data["state"] == "CONSUMED" then
		return -2
	end
	data["state"] = "CONSUMED"
	redis.call("set", KEYS[1], cjson.encode(data), "EX", 60)
	return 1
`)

type linkTokenPayload struct {
	Sub    string `json:"sub"`
	Email  string `json:"email"`
	UserID string `json:"user_id"`
	State  string `json:"state"`
}

// LinkGoogleAccount securely links a verified Google identity to an existing
// password-based account using a stateful, concurrency-safe, recoverable protocol.
func (s *Service) LinkGoogleAccount(ctx context.Context, email, password, linkToken string) (*UserDTO, *TokenPairDTO, error) {
	if email == "" || password == "" || linkToken == "" {
		return nil, nil, platformerrors.New(platformerrors.CodeInvalidArgument, "email, password, and link_token are required")
	}

	email = normalizeEmail(email)
	redisKey := "auth:google_link:" + linkToken

	// 1. Retrieve link token data from Redis (do NOT delete yet)
	dataStr, err := s.rdb.Get(ctx, redisKey).Result()
	if err != nil {
		s.log.Warn("Failed to retrieve or expired Google link token")
		return nil, nil, platformerrors.New(platformerrors.CodeInvalidArgument, "invalid or expired link token. Please initiate Google sign-in again.")
	}

	var payload linkTokenPayload
	if err := json.Unmarshal([]byte(dataStr), &payload); err != nil {
		s.log.Error("Failed to unmarshal link token payload", zap.Error(err))
		return nil, nil, platformerrors.New(platformerrors.CodeInternal, "failed to process link token")
	}

	// Reject immediately if token was already consumed (anti-replay check)
	if payload.State == "CONSUMED" {
		s.log.Warn("Replay attempt detected for consumed link token")
		// Check if the Google identity is already linked to the intended user
		existingIdent, checkErr := s.identityRepo.GetByProviderAndUID(ctx, "google", payload.Sub)
		if checkErr != nil {
			s.log.Error("Failed to lookup identity during consumed link token validation", zap.Error(checkErr))
			return nil, nil, fmt.Errorf("failed to verify account link status: %w", checkErr)
		}
		if existingIdent != nil && existingIdent.UserID == payload.UserID {
			return nil, nil, platformerrors.New(platformerrors.CodeAlreadyExists, "account is already linked. Please sign in with Google directly.")
		}
		return nil, nil, platformerrors.New(platformerrors.CodeInvalidArgument, "link token has already been used. Please initiate Google sign-in again.")
	}

	// 2. Verify target email matches the verified Google email
	if email != normalizeEmail(payload.Email) {
		s.log.Warn("Link email mismatch between submitted email and verified Google identity")
		return nil, nil, platformerrors.New(platformerrors.CodeInvalidArgument, "submitted email does not match the Google identity")
	}

	// 3. Retrieve local account
	u, err := s.userRepo.GetByID(ctx, payload.UserID)
	if err != nil {
		s.log.Error("Failed to fetch user during account linking", zap.String("userID", payload.UserID), zap.Error(err))
		return nil, nil, err
	}
	if u == nil {
		return nil, nil, platformerrors.New(platformerrors.CodeNotFound, "user account not found")
	}

	// 4. Account status checks (reject pending verification, suspended or banned)
	if u.Status == "PENDING_VERIFICATION" {
		s.log.Warn("LinkGoogleAccount rejected: account pending verification", zap.String("userID", u.ID))
		return nil, nil, platformerrors.New(platformerrors.CodeAccountNotActive, "account is not verified. Please verify your email first.")
	}
	if u.Status == "SUSPENDED" || u.Status == "BANNED" {
		return nil, nil, platformerrors.New(platformerrors.CodePermissionDenied, fmt.Sprintf("your account has been %s", u.Status))
	}

	// 5. Passwordless accounts cannot be linked via password
	if u.PasswordHash == "" {
		s.log.Warn("LinkGoogleAccount rejected: account has no password", zap.String("userID", u.ID))
		return nil, nil, platformerrors.New(platformerrors.CodeFailedPrecondition, "account has no password configured. Please sign in with Google directly.")
	}

	// 6. Check brute-force lockout
	now := time.Now().UTC()
	if u.LockedUntil != nil && u.LockedUntil.After(now) {
		return nil, nil, platformerrors.New(platformerrors.CodeFailedPrecondition, "account is temporarily locked due to too many failed attempts")
	}

	// 7. Verify local password BEFORE attempting state transition or token consumption
	if err := s.compareBcrypt(u.PasswordHash, password); err != nil {
		failedCount := u.FailedLoginAttempts + 1
		var lockUntil *time.Time
		if failedCount >= 5 {
			l := now.Add(15 * time.Minute)
			lockUntil = &l
		}
		_ = s.userRepo.TrackFailedLogin(ctx, u.ID, failedCount, lockUntil)
		// NOTE: Token remains valid in Redis so the user can re-try typing their password without restarting Google OAuth
		return nil, nil, platformerrors.New(platformerrors.CodeInvalidCredentials, "incorrect account password")
	}

	// 8. CONCURRENCY CONTROL: Acquire an exclusive lease with a unique owner token (15s lease)
	ownerToken := uuid.NewString()
	lockKey := "auth:google_link_lock:" + linkToken
	acquired, err := s.rdb.SetNX(ctx, lockKey, ownerToken, 15*time.Second).Result()
	if err != nil || !acquired {
		s.log.Warn("Concurrent account linking attempt detected", zap.String("userID", u.ID))
		return nil, nil, platformerrors.New(platformerrors.CodeFailedPrecondition, "account linking is currently being processed. Please wait a moment.")
	}
	defer func() {
		// Atomic compare-and-delete: releases lock ONLY if this request still owns it.
		// Dedicated 2-second timeout context ensures cleanup runs even if opCtx was canceled or timed out.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_ = compareAndDeleteScript.Run(cleanupCtx, s.rdb, []string{lockKey}, ownerToken).Err()
	}()

	// Bounded operation context: strictly terminates any slow DB or RPC operation within 8 seconds,
	// ensuring the critical section completes well before the 15-second Redis lock lease could expire.
	opCtx, opCancel := context.WithTimeout(ctx, 8*time.Second)
	defer opCancel()

	// 9. Check existing identity binding (idempotent recovery)
	existingIdent, err := s.identityRepo.GetByProviderAndUID(opCtx, "google", payload.Sub)
	if err != nil {
		s.log.Error("Failed to check existing identity during link", zap.String("userID", u.ID), zap.Error(err))
		return nil, nil, err
	}
	if existingIdent != nil && existingIdent.UserID != u.ID {
		return nil, nil, platformerrors.New(platformerrors.CodeAlreadyExists, "this Google account is already linked to a different TradeDrift profile")
	}

	// 10. Persist identity link if not already linked
	if existingIdent == nil {
		newIdentity := &repository.UserAuthIdentity{
			ID:             uuid.NewString(),
			UserID:         u.ID,
			Provider:       "google",
			ProviderUserID: payload.Sub,
			Email:          payload.Email,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := s.identityRepo.Create(opCtx, newIdentity); err != nil {
			s.log.Error("Failed to save linked Google identity", zap.String("userID", u.ID), zap.Error(err))
			// On DB failure, return error without consuming the token, allowing user to retry
			return nil, nil, err
		}
	}

	// 11. Durable wallet readiness verification (fail-closed)
	if err := s.walletCl.InitializeWallet(opCtx, u.ID); err != nil {
		s.log.Error("Failed to verify/initialize wallet during account linking", zap.String("userID", u.ID), zap.Error(err))
		return nil, nil, platformerrors.Wrap(err, platformerrors.CodeInternal, "account linked, but wallet initialization failed. Please retry sign-in.")
	}

	// 12. ATOMIC TOKEN CONSUMPTION: Transitions token to CONSUMED with 60s TTL via Lua
	res, err := consumeLinkTokenScript.Run(opCtx, s.rdb, []string{redisKey}).Int()
	if err != nil || res != 1 {
		s.log.Warn("Atomic link token consumption rejected", zap.Int("res", res), zap.Error(err))
		if res == -2 {
			return nil, nil, platformerrors.New(platformerrors.CodeInvalidArgument, "link token has already been used")
		}
		return nil, nil, platformerrors.New(platformerrors.CodeInvalidArgument, "link token was already consumed or expired")
	}

	// 13. Issue standard TradeDrift session tokens
	tokens, err := s.issueTokenPair(opCtx, u.ID, u.Email, u.Role, u.TokenVersion)
	if err != nil {
		s.log.Error("Failed to issue session tokens after linking", zap.String("userID", u.ID), zap.Error(err))
		return nil, nil, err
	}

	_ = s.userRepo.ResetFailedLogin(ctx, u.ID, now)

	s.log.Info("Successfully linked Google identity to local account",
		zap.String("userID", u.ID),
	)

	return &UserDTO{
		ID:       u.ID,
		Email:    u.Email,
		Username: u.Username,
	}, tokens, nil
}

// ConnectGoogleAccount connects a Google identity to the currently authenticated user session.
func (s *Service) ConnectGoogleAccount(ctx context.Context, userID, idToken string) (string, error) {
	if s.googleValidator == nil {
		return "", platformerrors.New(platformerrors.CodeInternal, "google authentication is temporarily unavailable")
	}

	payload, err := s.googleValidator.Validate(ctx, idToken)
	if err != nil {
		return "", platformerrors.New(platformerrors.CodeInvalidCredentials, "invalid or expired Google token")
	}

	payload.Email = normalizeEmail(payload.Email)

	// 1. Verify Google sub is not already linked to anyone
	existing, err := s.identityRepo.GetByProviderAndUID(ctx, "google", payload.Subject)
	if err != nil {
		return "", err
	}
	if existing != nil {
		if existing.UserID == userID {
			// Already connected to this user — verify wallet readiness before returning success!
			if err := s.walletCl.InitializeWallet(ctx, userID); err != nil {
				s.log.Error("Failed to verify wallet readiness during idempotent connect", zap.String("userID", userID), zap.Error(err))
				return "", platformerrors.Wrap(err, platformerrors.CodeInternal, "failed to verify wallet readiness; please try again")
			}
			return payload.Email, nil
		}
		return "", platformerrors.New(platformerrors.CodeAlreadyExists, "this Google account is already linked to another TradeDrift profile")
	}

	// 2. Verify user doesn't already have a Google identity
	current, err := s.identityRepo.GetByUserAndProvider(ctx, userID, "google")
	if err != nil {
		return "", err
	}
	if current != nil {
		return "", platformerrors.New(platformerrors.CodeAlreadyExists, "your account already has a connected Google account")
	}

	// 3. Create identity link
	now := time.Now().UTC()
	newIdentity := &repository.UserAuthIdentity{
		ID:             uuid.NewString(),
		UserID:         userID,
		Provider:       "google",
		ProviderUserID: payload.Subject,
		Email:          payload.Email,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.identityRepo.Create(ctx, newIdentity); err != nil {
		return "", err
	}

	// 4. Verify wallet readiness (fail-closed)
	if err := s.walletCl.InitializeWallet(ctx, userID); err != nil {
		s.log.Error("Failed to verify/initialize wallet during Google account connect", zap.String("userID", userID), zap.Error(err))
		return "", platformerrors.Wrap(err, platformerrors.CodeInternal, "failed to verify wallet readiness; please try again")
	}

	s.log.Info("Connected Google identity from user session",
		zap.String("userID", userID),
	)

	return payload.Email, nil
}
