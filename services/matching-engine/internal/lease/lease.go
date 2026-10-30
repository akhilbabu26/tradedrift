package lease

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrLockAcquisitionFailed = errors.New("failed to acquire partition advisory lock: already held by another instance")
	ErrLeaseLost             = errors.New("partition lease lost: database connection terminated")
)

// AdvisoryLockMagic is a deterministic 64-bit salt ("TD_ME" in ASCII) combined with partition ID.
const AdvisoryLockMagic int64 = 0x54445F4D45000000

// PartitionLockID computes the unique 64-bit PostgreSQL advisory lock ID for a given partition.
func PartitionLockID(partition int) int64 {
	return AdvisoryLockMagic | int64(partition&0xFFFFFF)
}

// Lease represents exclusive session ownership of a partition.
type Lease interface {
	Partition() int
	IsValid() bool
	Release(ctx context.Context) error
	Done() <-chan struct{}
}

// LockAcquirer provides partition lease acquisition.
type LockAcquirer interface {
	AcquireLease(ctx context.Context, partition int) (Lease, error)
}

// PGXPoolLeaseManager acquires exclusive PostgreSQL session-level advisory locks using dedicated pgxpool connections.
type PGXPoolLeaseManager struct {
	pool *pgxpool.Pool
}

// NewPGXPoolLeaseManager creates a lease manager backed by a PostgreSQL connection pool.
func NewPGXPoolLeaseManager(pool *pgxpool.Pool) *PGXPoolLeaseManager {
	return &PGXPoolLeaseManager{pool: pool}
}

// AcquireLease checks out a dedicated connection and attempts to acquire an exclusive session advisory lock.
func (m *PGXPoolLeaseManager) AcquireLease(ctx context.Context, partition int) (Lease, error) {
	if m.pool == nil {
		return nil, errors.New("nil pgxpool provided")
	}
	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire dedicated pgxpool connection for partition %d: %w", partition, err)
	}

	lockID := PartitionLockID(partition)
	var acquired bool
	err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&acquired)
	if err != nil {
		conn.Release()
		return nil, fmt.Errorf("execute pg_try_advisory_lock for partition %d: %w", partition, err)
	}
	if !acquired {
		conn.Release()
		return nil, fmt.Errorf("%w for partition %d", ErrLockAcquisitionFailed, partition)
	}

	pollCtx, pollCancel := context.WithCancel(context.Background())
	l := &leaseImpl{
		conn:       conn,
		partition:  partition,
		lockID:     lockID,
		done:       make(chan struct{}),
		isValid:    true,
		cancelPoll: pollCancel,
	}

	// Liveness monitor: periodically verify connection health.
	// If the database connection drops, PostgreSQL releases all session advisory locks.
	// The monitor detects this and marks the lease invalid, notifying consumers to fail closed.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(pollCtx, 1*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					log.Printf("[lease] FATAL: database connection lost for partition %d lease: %v", partition, err)
					_ = l.markLost()
					return
				}
			}
		}
	}()

	return l, nil
}

type leaseImpl struct {
	conn       *pgxpool.Conn
	partition  int
	lockID     int64
	done       chan struct{}
	closeOnce  sync.Once
	mu         sync.Mutex
	isValid    bool
	cancelPoll context.CancelFunc
}

func (l *leaseImpl) Partition() int {
	return l.partition
}

func (l *leaseImpl) IsValid() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.isValid
}

func (l *leaseImpl) Done() <-chan struct{} {
	return l.done
}

func (l *leaseImpl) markLost() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.isValid = false
		l.mu.Unlock()

		if l.cancelPoll != nil {
			l.cancelPoll()
		}
		close(l.done)
		if l.conn != nil {
			l.conn.Release()
			l.conn = nil
		}
	})
	return ErrLeaseLost
}

func (l *leaseImpl) Release(ctx context.Context) error {
	var releaseErr error
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.isValid = false
		l.mu.Unlock()

		if l.cancelPoll != nil {
			l.cancelPoll()
		}
		close(l.done)

		if l.conn != nil {
			unlockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_, releaseErr = l.conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", l.lockID)
			l.conn.Release()
			l.conn = nil
		}
	})
	return releaseErr
}

// MockLeaseManager provides deterministic in-memory lease simulation for testing.
type MockLeaseManager struct {
	mu           sync.Mutex
	heldLocks    map[int]*MockLease
	failPart     map[int]error
	AcquireHooks func(partition int)
}

// NewMockLeaseManager creates an in-memory lock acquirer.
func NewMockLeaseManager() *MockLeaseManager {
	return &MockLeaseManager{
		heldLocks: make(map[int]*MockLease),
		failPart:  make(map[int]error),
	}
}

func (m *MockLeaseManager) SetFailPartition(partition int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failPart[partition] = err
}

func (m *MockLeaseManager) AcquireLease(ctx context.Context, partition int) (Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.AcquireHooks != nil {
		m.AcquireHooks(partition)
	}

	if err, fail := m.failPart[partition]; fail && err != nil {
		return nil, err
	}

	if existing, held := m.heldLocks[partition]; held && existing.IsValid() {
		return nil, fmt.Errorf("%w for partition %d", ErrLockAcquisitionFailed, partition)
	}

	ml := &MockLease{
		partition: partition,
		done:      make(chan struct{}),
		isValid:   true,
		mgr:       m,
	}
	m.heldLocks[partition] = ml
	return ml, nil
}

// MockLease simulates a single partition lease.
type MockLease struct {
	mu        sync.Mutex
	partition int
	done      chan struct{}
	closeOnce sync.Once
	isValid   bool
	mgr       *MockLeaseManager
}

func (m *MockLease) Partition() int {
	return m.partition
}

func (m *MockLease) IsValid() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isValid
}

func (m *MockLease) Done() <-chan struct{} {
	return m.done
}

// Revoke simulates connection loss or lease expiration.
func (m *MockLease) Revoke() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.isValid = false
		m.mu.Unlock()
		close(m.done)
	})
}

// Release releases the lease.
func (m *MockLease) Release(ctx context.Context) error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.isValid = false
		m.mu.Unlock()
		close(m.done)
		if m.mgr != nil {
			m.mgr.mu.Lock()
			delete(m.mgr.heldLocks, m.partition)
			m.mgr.mu.Unlock()
		}
	})
	return nil
}
