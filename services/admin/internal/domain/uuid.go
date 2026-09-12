package domain

import "github.com/google/uuid"

// MustNewV7 generates a new UUIDv7 string. It panics if the underlying UUIDv7 generation fails.
// UUIDv7 is strictly time-ordered (Unix epoch timestamp in milliseconds in high bits),
// ensuring optimal B-tree insertion locality and sequential indexing in PostgreSQL.
func MustNewV7() string {
	u, err := uuid.NewV7()
	if err != nil {
		panic("domain: failed to generate UUIDv7: " + err.Error())
	}
	return u.String()
}
