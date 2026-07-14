package jwt

import (
	"context"
	stderrors "errors"
	"fmt"
	"log"
	"strconv"
	"time"

	golangjwt "github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"tradedrift/platform/errors"
)

const jwtIssuer = "tradedrift-auth"

// AuthProvider defines the interface to fetch backing state from PostgreSQL.
// This allows the platform validator to run cache-aside checks without importing DB libraries.
type AuthProvider interface {
	GetTokenVersion(ctx context.Context, userID string) (int, error)
	IsTokenBlacklisted(ctx context.Context, jti string) (bool, error)
}

// RedisValidator validates JWTs using:
// - Redis cache for token versions
// - Redis cache for JTI blacklist lookups
// - PostgreSQL as the authoritative source for token versions and durable JTI blacklist entries.
type RedisValidator struct {
	secret []byte
	rdb    *redis.Client
	ap     AuthProvider
}

// NewRedisValidator creates a new RedisValidator.
func NewRedisValidator(secret []byte, rdb *redis.Client, ap AuthProvider) *RedisValidator {
	return &RedisValidator{
		secret: secret,
		rdb:    rdb,
		ap:     ap,
	}
}

// Validate parses, verifies signature, and executes blacklist/token-version checks on the access token.
func (v *RedisValidator) Validate(ctx context.Context, tokenStr string) (*Claims, error) {
	parser := golangjwt.NewParser(
		golangjwt.WithIssuer(jwtIssuer),
		golangjwt.WithLeeway(30*time.Second),
	)

	token, err := parser.ParseWithClaims(tokenStr, &Claims{}, func(t *golangjwt.Token) (interface{}, error) {
		if t.Method.Alg() != "HS256" {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return v.secret, nil
	})

	if err != nil {
		if stderrors.Is(err, golangjwt.ErrTokenExpired) {
			return nil, errors.New(errors.CodeTokenExpired, "token has expired")
		}
		return nil, errors.Wrap(err, errors.CodeInvalidCredentials, "invalid token signature or format")
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New(errors.CodeInvalidCredentials, "invalid token claims")
	}

	// Validate presence of required claims to avoid potential nil or empty values
	if claims.UserID == "" || claims.Email == "" || claims.JTI == "" || claims.IssuedAt == nil || claims.Subject == "" {
		return nil, errors.New(errors.CodeInvalidCredentials, "missing required claims")
	}

	// Ensure Subject agrees with UserID
	if claims.Subject != claims.UserID {
		return nil, errors.New(errors.CodeInvalidCredentials, "subject does not match user id")
	}

	// Ensure iat is not in the future (allowing for 60 seconds clock skew)
	if claims.IssuedAt.Time.After(time.Now().UTC().Add(time.Minute)) {
		return nil, errors.New(errors.CodeInvalidCredentials, "invalid issued-at claim")
	}

	// 1. Validate Token Version (for global revocation: LogoutAll, ChangePassword)
	activeVersion, err := v.getActiveTokenVersion(ctx, claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve active token version: %w", err)
	}
	if claims.TokenVersion != activeVersion {
		return nil, errors.New(errors.CodeTokenRevoked, "token has been revoked")
	}

	// 2. Check JTI token-level blacklist (for single session logout)
	blacklisted, err := v.isTokenBlacklisted(ctx, claims.JTI, claims.ExpiresAt.Time)
	if err != nil {
		return nil, fmt.Errorf("failed to verify token blacklist status: %w", err)
	}
	if blacklisted {
		return nil, errors.New(errors.CodeTokenRevoked, "token has been revoked")
	}

	return claims, nil
}

// getActiveTokenVersion performs cache-aside lookup for the active token version.
func (v *RedisValidator) getActiveTokenVersion(ctx context.Context, userID string) (int, error) {
	cacheKey := "auth:token_version:" + userID

	// Try Redis cache
	val, err := v.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		version, parseErr := strconv.Atoi(val)
		if parseErr == nil {
			return version, nil
		}
	} else if err != redis.Nil {
		log.Printf("Warning: Redis connection error during token version check: %v. Falling back to PostgreSQL.", err)
	}

	// Cache miss -> Fetch from PostgreSQL via AuthProvider
	version, err := v.ap.GetTokenVersion(ctx, userID)
	if err != nil {
		return 0, err
	}

	// Populate Redis cache
	_ = v.rdb.Set(ctx, cacheKey, strconv.Itoa(version), 24*time.Hour).Err()

	return version, nil
}

// isTokenBlacklisted performs cache-aside lookup using explicit negative caching ("valid"/"revoked").
func (v *RedisValidator) isTokenBlacklisted(ctx context.Context, jti string, expiresAt time.Time) (bool, error) {
	cacheKey := "token:blacklist:" + jti

	// Check Redis cache
	val, err := v.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		if val == "revoked" {
			return true, nil
		}
		if val == "valid" {
			return false, nil
		}
	} else if err != redis.Nil {
		log.Printf("Warning: Redis connection error during JTI blacklist check: %v. Falling back to PostgreSQL.", err)
	}

	// Cache miss -> Query PostgreSQL via AuthProvider
	blacklisted, err := v.ap.IsTokenBlacklisted(ctx, jti)
	if err != nil {
		return false, err
	}

	// Calculate remaining token TTL (must be at least 1 second to write cache)
	ttl := time.Until(expiresAt)
	if ttl < time.Second {
		ttl = time.Second
	}

	// Write negative/positive cache entry to Redis
	cacheVal := "valid"
	if blacklisted {
		cacheVal = "revoked"
	}
	_ = v.rdb.Set(ctx, cacheKey, cacheVal, ttl).Err()

	return blacklisted, nil
}
