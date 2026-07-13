package jwt

import "context"

// Zero-sized struct context key is impossible to collide with other package keys.
type claimsContextKey struct{}

// WithClaims returns a new context with the validated JWT claims.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// FromContext retrieves the JWT claims from the context, if present.
func FromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey{}).(*Claims)
	return claims, ok
}
