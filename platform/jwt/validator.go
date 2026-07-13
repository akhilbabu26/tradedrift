package jwt

import "context"

// Validator defines the abstraction for JWT validation logic.
// This allows transport layers to remain independent of the storage layer (Redis, SQL, etc.).
type Validator interface {
	Validate(ctx context.Context, tokenStr string) (*Claims, error)
}
