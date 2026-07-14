package jwt

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	golangjwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// IssueAccessToken creates and signs a new JWT access token with HS256.
// It returns the signed token string, the generated JTI string, and any error.
func IssueAccessToken(userID, email string, tokenVersion int, secret []byte, ttl time.Duration) (string, string, error) {
	jti := uuid.NewString() // Generate a unique identifier for this specific token instance

	now := time.Now().UTC()
	claims := Claims{
		UserID:       userID,
		Email:        email,
		JTI:          jti,
		TokenVersion: tokenVersion,
		RegisteredClaims: golangjwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    jwtIssuer,
			ExpiresAt: golangjwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  golangjwt.NewNumericDate(now),
			NotBefore: golangjwt.NewNumericDate(now),
		},
	}

	token := golangjwt.NewWithClaims(golangjwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(secret)
	if err != nil {
		return "", "", fmt.Errorf("failed to sign access token: %w", err)
	}

	return tokenStr, jti, nil
}

// IssueRefreshToken generates a secure, random opaque token.
// It returns the raw token (to be sent to the client) and its SHA-256 hash (to be stored in the DB).
func IssueRefreshToken() (string, string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes for refresh token: %w", err)
	}

	rawToken := hex.EncodeToString(bytes)

	// Hash the raw token for storage at rest
	hash := sha256.Sum256([]byte(rawToken))
	hashedToken := hex.EncodeToString(hash[:])

	return rawToken, hashedToken, nil
}
