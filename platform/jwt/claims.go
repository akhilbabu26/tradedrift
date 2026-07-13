package jwt

import golangjwt "github.com/golang-jwt/jwt/v5"

// Claims represents the standard and custom claims contained in a TradeDrift JWT.
type Claims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	JTI    string `json:"jti"` // Unique token identifier used for blacklist checks
	golangjwt.RegisteredClaims
}
