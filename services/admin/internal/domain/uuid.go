package domain

import (
	platformuuid "tradedrift/platform/uuid"
)

// MustNewV7 generates a new UUIDv7 string. It panics if the underlying UUIDv7 generation fails.
// UUIDv7 is strictly time-ordered (Unix epoch timestamp in milliseconds in high bits),
// ensuring optimal B-tree insertion locality and sequential indexing in PostgreSQL.
func MustNewV7() string {
	id, err := platformuuid.New()
	if err != nil {
		panic("domain: failed to generate UUIDv7: " + err.Error())
	}
	return id
}
