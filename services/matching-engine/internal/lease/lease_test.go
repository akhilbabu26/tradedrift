package lease_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tradedrift/services/matching-engine/internal/lease"
)

func TestPartitionLockID_DeterministicAndPositive(t *testing.T) {
	id0 := lease.PartitionLockID(0)
	id1 := lease.PartitionLockID(1)
	id2 := lease.PartitionLockID(2)

	if id0 <= 0 || id1 <= 0 || id2 <= 0 {
		t.Fatalf("expected positive 64-bit lock IDs, got %d, %d, %d", id0, id1, id2)
	}
	if id0 == id1 || id1 == id2 || id0 == id2 {
		t.Fatalf("expected distinct lock IDs for distinct partitions, got %d, %d, %d", id0, id1, id2)
	}
	// Verify magic prefix preservation
	if id0&lease.AdvisoryLockMagic != lease.AdvisoryLockMagic {
		t.Fatalf("expected magic prefix in lock ID, got 0x%X", id0)
	}
}

func TestMockLeaseManager_SingleOwnerExclusive(t *testing.T) {
	mgr := lease.NewMockLeaseManager()
	ctx := context.Background()

	// 1. Instance A acquires lease on Partition 0
	leaseA, err := mgr.AcquireLease(ctx, 0)
	if err != nil {
		t.Fatalf("unexpected error acquiring lease A: %v", err)
	}
	if !leaseA.IsValid() {
		t.Fatal("expected lease A to be valid")
	}

	// 2. Instance B attempts to acquire lease on Partition 0 — must fail
	leaseB, err := mgr.AcquireLease(ctx, 0)
	if err == nil {
		t.Fatal("expected error acquiring already-held lease B, got nil")
	}
	if !errors.Is(err, lease.ErrLockAcquisitionFailed) {
		t.Fatalf("expected ErrLockAcquisitionFailed, got: %v", err)
	}
	if leaseB != nil {
		t.Fatal("expected nil lease on failed acquisition")
	}

	// 3. Different partition (Partition 1) can be acquired without conflict
	leaseC, err := mgr.AcquireLease(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error acquiring partition 1 lease: %v", err)
	}
	if !leaseC.IsValid() {
		t.Fatal("expected partition 1 lease to be valid")
	}
}

func TestMockLease_RevokeAndDoneNotification(t *testing.T) {
	mgr := lease.NewMockLeaseManager()
	ctx := context.Background()

	l, err := mgr.AcquireLease(ctx, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ml, ok := l.(*lease.MockLease)
	if !ok {
		t.Fatal("expected *lease.MockLease")
	}

	// Simulate connection loss / lock eviction
	ml.Revoke()

	if ml.IsValid() {
		t.Fatal("expected lease to be invalid after revocation")
	}

	select {
	case <-ml.Done():
		// Expected: Done() channel is closed immediately
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected Done() channel to close upon revocation")
	}
}

func TestMockLease_ReleaseAndReacquisition(t *testing.T) {
	mgr := lease.NewMockLeaseManager()
	ctx := context.Background()

	// 1. Acquire
	l1, err := mgr.AcquireLease(ctx, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2. Release cleanly (e.g. during graceful shutdown)
	if err := l1.Release(ctx); err != nil {
		t.Fatalf("unexpected error releasing lease: %v", err)
	}
	if l1.IsValid() {
		t.Fatal("expected released lease to be invalid")
	}

	// 3. Re-acquire (e.g. restarted instance)
	l2, err := mgr.AcquireLease(ctx, 0)
	if err != nil {
		t.Fatalf("unexpected error re-acquiring lease: %v", err)
	}
	if !l2.IsValid() {
		t.Fatal("expected re-acquired lease to be valid")
	}
}
