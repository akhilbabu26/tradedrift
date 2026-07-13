package jwt

import (
	"context"
	stderrors "errors"
	"fmt"
	"strconv"
	"time"

	golangjwt "github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"tradedrift/platform/errors"
)

const jwtIssuer = "tradedrift-auth"

// RedisValidator implements jwt.Validator using a Redis blacklist.
type RedisValidator struct {
	secret []byte
	rdb    *redis.Client
}

// NewRedisValidator creates a new RedisValidator.
func NewRedisValidator(secret []byte, rdb *redis.Client) *RedisValidator {
	return &RedisValidator{
		secret: secret,
		rdb:    rdb,
	}
}

// Validate parses, verifies signature, and executes blacklist checks on the access token.
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

	// Check JTI token-level blacklist (for single session logout)
	blacklistedJTIKey := "token:blacklist:" + claims.JTI
	exists, err := v.rdb.Exists(ctx, blacklistedJTIKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to query redis for token blacklist: %w", err)
	}
	if exists > 0 {
		return nil, errors.New(errors.CodeTokenRevoked, "token has been revoked")
	}

	// Check user-level blacklist (for LogoutAll and ChangePassword)
	blacklistedUserKey := "user:blacklist:" + claims.UserID
	val, err := v.rdb.Get(ctx, blacklistedUserKey).Result()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("failed to query redis for user blacklist: %w", err)
	}
	if err == nil {
		// Parse the stored revocation Unix timestamp
		revocationTimeUnix, parseErr := strconv.ParseInt(val, 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse user blacklist timestamp: %w", parseErr)
		}

		// If this token was issued BEFORE the global logout event, it is revoked
		if claims.IssuedAt.Unix() < revocationTimeUnix {
			return nil, errors.New(errors.CodeTokenRevoked, "user sessions have been revoked")
		}
	}

	return claims, nil
}
