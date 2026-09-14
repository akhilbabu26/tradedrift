package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"tradedrift/services/admin/internal/client"
	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

// AdminService orchestrates all administrative mutations.
// It enforces atomic audit consistency, caller idempotency, event publication via outbox,
// and safe reconciliation across downstream services (Auth, Wallet).
type AdminService struct {
	txMgr     repository.TxManager
	opsRepo   repository.OperationsRepository
	authCli   *client.AuthClient
	walletCli *client.WalletClient
	rdb       redis.Cmdable
	log       *zap.Logger
}

// NewAdminService constructs the core admin service.
func NewAdminService(
	txMgr repository.TxManager,
	opsRepo repository.OperationsRepository,
	authCli *client.AuthClient,
	walletCli *client.WalletClient,
	rdb redis.Cmdable,
	log *zap.Logger,
) *AdminService {
	return &AdminService{
		txMgr:     txMgr,
		opsRepo:   opsRepo,
		authCli:   authCli,
		walletCli: walletCli,
		rdb:       rdb,
		log:       log,
	}
}

func (s *AdminService) SetRedisWithRetry(ctx context.Context, key, val string, maxRetries int) error {
	if s.rdb == nil {
		return errors.New("redis client uninitialized")
	}
	var lastErr error
	backoffs := []time.Duration{50 * time.Millisecond, 150 * time.Millisecond, 300 * time.Millisecond}
	for i := 0; i <= maxRetries; i++ {
		err := s.rdb.Set(ctx, key, val, 0).Err()
		if err == nil {
			return nil
		}
		lastErr = err
		if i < len(backoffs) {
			time.Sleep(backoffs[i])
		}
	}
	return lastErr
}

func (s *AdminService) DelRedisWithRetry(ctx context.Context, key string, maxRetries int) error {
	if s.rdb == nil {
		return errors.New("redis client uninitialized")
	}
	var lastErr error
	backoffs := []time.Duration{50 * time.Millisecond, 150 * time.Millisecond, 300 * time.Millisecond}
	for i := 0; i <= maxRetries; i++ {
		err := s.rdb.Del(ctx, key).Err()
		if err == nil {
			return nil
		}
		lastErr = err
		if i < len(backoffs) {
			time.Sleep(backoffs[i])
		}
	}
	return lastErr
}

// ─── Shared Helpers ───────────────────────────────────────────────────────────

// resolveConcurrentConflict is called when a Postgres unique-constraint violation (23505)
// is detected during an operation insert, meaning a concurrent request already committed
// the same idempotency key. It re-fetches the winning operation and returns it.
func (s *AdminService) resolveConcurrentConflict(ctx context.Context, adminID, key string) (*domain.AdminOperation, error) {
	existing, err := s.opsRepo.GetByIdempotencyKey(ctx, adminID, key)
	if err != nil {
		return nil, fmt.Errorf("resolveConcurrentConflict: lookup failed: %w", err)
	}
	if existing != nil {
		if existing.Status == domain.OperationStatusProcessing {
			return nil, domain.ErrOperationInProgress
		}
		return existing, nil
	}
	return nil, domain.ErrOperationInProgress
}

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
