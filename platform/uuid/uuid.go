package uuid

import (
	"fmt"

	"github.com/google/uuid"
)

// New generates a new UUIDv7 string. 
// It returns an error if the system clock returns a timestamp before the Unix epoch
// or if the underlying entropy pool fails to read.
func New() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("failed to generate UUIDv7: %w", err)
	}
	return id.String(), nil
}
