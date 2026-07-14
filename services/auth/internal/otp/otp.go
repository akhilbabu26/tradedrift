package otp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrMaxAttemptsExceeded = errors.New("maximum OTP verification attempts exceeded")
	ErrOTPNotFound         = errors.New("OTP verification code not found or expired")
)

type Manager struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewManager creates a new OTP Manager.
func NewManager(rdb *redis.Client, ttl time.Duration) *Manager {
	return &Manager{
		rdb: rdb,
		ttl: ttl,
	}
}

// Generate generates a cryptographically secure 6-digit OTP code,
// saves it in Redis under the given key, and resets failed attempt counters.
func (m *Manager) Generate(ctx context.Context, key string) (string, error) {
	// 1. Generate secure 6-digit numeric OTP code
	code, err := generateSecureNumericCode(6)
	if err != nil {
		return "", fmt.Errorf("failed to generate secure code: %w", err)
	}

	codeKey := "otp:code:" + key
	attemptsKey := "otp:attempts:" + key

	// 2. Store OTP code and reset failed attempts in Redis using a transaction (pipeline)
	pipe := m.rdb.TxPipeline()
	pipe.Set(ctx, codeKey, code, m.ttl)
	pipe.Set(ctx, attemptsKey, "0", m.ttl) // Tracks attempts for this specific code

	_, err = pipe.Exec(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to store OTP keys in redis: %w", err)
	}

	return code, nil
}

// Verify validates the OTP code against Redis. If it fails 5 times, the OTP is deleted.
func (m *Manager) Verify(ctx context.Context, key string, inputCode string) (bool, error) {
	codeKey := "otp:code:" + key
	attemptsKey := "otp:attempts:" + key

	// 1. Retrieve the correct code from Redis
	storedCode, err := m.rdb.Get(ctx, codeKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, ErrOTPNotFound
		}
		return false, fmt.Errorf("failed to fetch OTP from redis: %w", err)
	}

	// 2. Compare the input code
	if storedCode == inputCode {
		// Clean up keys on successful verification
		_ = m.rdb.Del(ctx, codeKey, attemptsKey).Err()
		return true, nil
	}

	// 3. Code is incorrect -> Increment failed attempts counter
	attempts, err := m.rdb.Incr(ctx, attemptsKey).Result()
	if err != nil {
		return false, fmt.Errorf("failed to increment OTP attempt counter: %w", err)
	}

	// 4. Check if brute-force limit is exceeded (max 5 attempts)
	if attempts >= 5 {
		// Lockout this code entirely by deleting it
		_ = m.rdb.Del(ctx, codeKey, attemptsKey).Err()
		return false, ErrMaxAttemptsExceeded
	}

	return false, nil
}

// generateSecureNumericCode creates a cryptographically secure numeric string of specified length.
func generateSecureNumericCode(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("invalid code length")
	}

	maxVal := big.NewInt(1)
	for i := 0; i < length; i++ {
		maxVal.Mul(maxVal, big.NewInt(10))
	}

	n, err := rand.Int(rand.Reader, maxVal)
	if err != nil {
		return "", err
	}

	// Format with leading zeros if necessary
	format := fmt.Sprintf("%%0%dd", length)
	return fmt.Sprintf(format, n.Int64()), nil
}

// This package is responsible for:
// Generating cryptographically secure 6-digit verification codes.
// Storing the codes in Redis with an expiration TTL (e.g., 5 minutes).
// Verifying the codes.
// Brute-force protection: Limiting attempts to a maximum of 5. If verification fails 5 times, the OTP is instantly deleted from Redis.
