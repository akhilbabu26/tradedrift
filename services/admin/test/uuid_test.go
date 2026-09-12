package test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"tradedrift/services/admin/internal/domain"
)

func TestMustNewV7(t *testing.T) {
	id1 := domain.MustNewV7()
	if id1 == "" {
		t.Fatal("expected non-empty UUIDv7")
	}

	parsed1, err := uuid.Parse(id1)
	if err != nil {
		t.Fatalf("failed to parse generated UUID: %v", err)
	}

	if parsed1.Version() != 7 {
		t.Fatalf("expected UUID version 7, got %d", parsed1.Version())
	}

	// Verify monotonic timestamp progression
	time.Sleep(2 * time.Millisecond)
	id2 := domain.MustNewV7()
	parsed2, err := uuid.Parse(id2)
	if err != nil {
		t.Fatalf("failed to parse second UUID: %v", err)
	}

	if id1 >= id2 {
		t.Fatalf("expected id1 < id2 for monotonic UUIDv7, got %s >= %s", id1, id2)
	}

	if parsed2.Version() != 7 {
		t.Fatalf("expected UUID version 7, got %d", parsed2.Version())
	}
}
