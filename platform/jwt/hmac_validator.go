package jwt

import (
	"context"
	"fmt"
	"time"

	golangjwt "github.com/golang-jwt/jwt/v5"
)

// HMACValidator is a stateless JWT validator that only verifies
// the HMAC-SHA256 signature and expiry. It does NOT check Redis
// revocation — suitable for edge services (e.g. API gateway) that
// trust the auth service to handle revocation on its end.
type HMACValidator struct {
	secret []byte
}

// NewHMACValidator creates a stateless HMACValidator.
func NewHMACValidator(secret []byte) *HMACValidator {
	return &HMACValidator{secret: secret}
}

func (v *HMACValidator) Validate(_ context.Context, tokenStr string) (*Claims, error) {
	const issuer = "tradedrift-auth"

	parser := golangjwt.NewParser(
		golangjwt.WithIssuer(issuer),
		golangjwt.WithLeeway(30*time.Second),
	)

	token, err := parser.ParseWithClaims(tokenStr, &Claims{}, func(t *golangjwt.Token) (interface{}, error) {
		if t.Method.Alg() != "HS256" {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return v.secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}
