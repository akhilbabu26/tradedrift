package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"tradedrift/services/admin/internal/client"
	"tradedrift/services/admin/internal/repository"
)

// StateReconciler ensures eventual convergence between PostgreSQL truth and Redis cache.
// It detects and heals missing keys and stale leftover keys using non-blocking SCAN.
// It also manages the market:enforcement:ready readiness sentinel to prevent cold-start order acceptance.
type StateReconciler struct {
	opsRepo   repository.OperationsRepository
	authCli   *client.AuthClient
	rdb       redis.Cmdable
	interval  time.Duration
	log       *zap.Logger
	done      chan struct{}
	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
}

// NewStateReconciler constructs a new StateReconciler.
func NewStateReconciler(
	opsRepo repository.OperationsRepository,
	authCli *client.AuthClient,
	rdb redis.Cmdable,
	interval time.Duration,
	log *zap.Logger,
) *StateReconciler {
	return &StateReconciler{
		opsRepo:  opsRepo,
		authCli:  authCli,
		rdb:      rdb,
		interval: interval,
		log:      log,
		done:     make(chan struct{}),
	}
}

// Start launches the periodic anti-drift reconciliation loop.
func (r *StateReconciler) Start(ctx context.Context) {
	r.startOnce.Do(func() {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			ticker := time.NewTicker(r.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := r.ReconcileOnce(ctx); err != nil {
						r.log.Error("StateReconciler: periodic cycle error", zap.Error(err))
					}
				case <-ctx.Done():
					return
				case <-r.done:
					return
				}
			}
		}()
	})
}

// Stop terminates the reconciliation loop gracefully.
func (r *StateReconciler) Stop() {
	r.stopOnce.Do(func() {
		close(r.done)
	})
	r.wg.Wait()
}

// ReconcileOnce runs a single pass of bi-directional market and user reconciliation.
func (r *StateReconciler) ReconcileOnce(ctx context.Context) error {
	if r.rdb == nil {
		return errors.New("redis client is uninitialized")
	}

	r.log.Debug("StateReconciler: starting anti-drift reconciliation pass")

	// 1. Reconcile Market Halts
	if err := r.reconcileMarkets(ctx); err != nil {
		return fmt.Errorf("reconcile markets: %w", err)
	}

	// 2. Reconcile Suspended Users
	if err := r.reconcileUsers(ctx); err != nil {
		return fmt.Errorf("reconcile users: %w", err)
	}

	return nil
}

// reconcileMarkets syncs PostgreSQL market halt status into Redis and sets market:enforcement:ready.
func (r *StateReconciler) reconcileMarkets(ctx context.Context) error {
	// A. Fetch Authoritative Market Snapshots from PostgreSQL
	snapshots, err := r.opsRepo.GetLatestMarketStates(ctx)
	if err != nil {
		return fmt.Errorf("fetch latest market states: %w", err)
	}

	expectedHalted := make(map[string]bool)
	for _, snap := range snapshots {
		expectedHalted[snap.MarketID] = snap.IsHalted
	}

	// B. Discover Cached Keys in Redis using Non-Blocking SCAN
	redisKeys := make(map[string]struct{})
	var cursor uint64
	for {
		keys, nextCursor, scanErr := r.rdb.Scan(ctx, cursor, "market:halted:*", 100).Result()
		if scanErr != nil {
			return fmt.Errorf("scan market:halted:* failed: %w", scanErr)
		}
		for _, k := range keys {
			redisKeys[k] = struct{}{}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// C. Bi-directional Convergence
	// 1. Ensure all expected halted markets exist in Redis; remove unhalted ones
	for marketID, isHalted := range expectedHalted {
		key := "market:halted:" + marketID
		_, inRedis := redisKeys[key]
		delete(redisKeys, key) // Processed

		if isHalted && !inRedis {
			if setErr := r.rdb.Set(ctx, key, "1", 0).Err(); setErr != nil {
				r.log.Error("reconciler: failed to set missing market halt key", zap.String("key", key), zap.Error(setErr))
			} else {
				r.log.Info("reconciler: restored missing market halt key", zap.String("key", key))
			}
		} else if !isHalted && inRedis {
			if delErr := r.rdb.Del(ctx, key).Err(); delErr != nil {
				r.log.Error("reconciler: failed to delete stale market halt key", zap.String("key", key), zap.Error(delErr))
			} else {
				r.log.Info("reconciler: deleted stale market halt key", zap.String("key", key))
			}
		}
	}

	// 2. Delete any leftover Redis keys not matching any authoritative market
	for staleKey := range redisKeys {
		if delErr := r.rdb.Del(ctx, staleKey).Err(); delErr != nil {
			r.log.Error("reconciler: failed to delete orphaned market halt key", zap.String("key", staleKey), zap.Error(delErr))
		} else {
			r.log.Info("reconciler: deleted orphaned market halt key", zap.String("key", staleKey))
		}
	}

	// D. Assert Market Enforcement Readiness Sentinel
	if err := r.rdb.Set(ctx, "market:enforcement:ready", "1", 0).Err(); err != nil {
		return fmt.Errorf("failed to assert market:enforcement:ready sentinel: %w", err)
	}

	return nil
}

// reconcileUsers syncs suspended users from Auth service into Redis.
func (r *StateReconciler) reconcileUsers(ctx context.Context) error {
	// A. Bounded Batch Fetch of Suspended Users from Auth service via internal gRPC
	suspendedSet := make(map[string]struct{})
	var offset int32 = 0
	const limit int32 = 1000

	for {
		userIDs, total, err := r.authCli.ListSuspendedUsers(ctx, limit, offset)
		if err != nil {
			return fmt.Errorf("list suspended users from auth service: %w", err)
		}
		for _, uid := range userIDs {
			suspendedSet[uid] = struct{}{}
		}
		offset += int32(len(userIDs))
		if offset >= total || len(userIDs) == 0 {
			break
		}
	}

	// B. Discover Cached Suspended Keys in Redis using Non-Blocking SCAN
	redisKeys := make(map[string]struct{})
	var cursor uint64
	for {
		keys, nextCursor, scanErr := r.rdb.Scan(ctx, cursor, "user:suspended:*", 100).Result()
		if scanErr != nil {
			return fmt.Errorf("scan user:suspended:* failed: %w", scanErr)
		}
		for _, k := range keys {
			redisKeys[k] = struct{}{}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// C. Bi-directional Convergence
	// 1. Ensure all suspended users exist in Redis
	for uid := range suspendedSet {
		key := "user:suspended:" + uid
		_, inRedis := redisKeys[key]
		delete(redisKeys, key) // Processed

		if !inRedis {
			if setErr := r.rdb.Set(ctx, key, "1", 0).Err(); setErr != nil {
				r.log.Error("reconciler: failed to set missing user suspended key", zap.String("key", key), zap.Error(setErr))
			} else {
				r.log.Info("reconciler: restored missing user suspended key", zap.String("key", key))
			}
		}
	}

	// 2. Delete any leftover Redis keys (users who have been unsuspended or orphaned)
	for staleKey := range redisKeys {
		if strings.HasPrefix(staleKey, "user:suspended:") {
			if delErr := r.rdb.Del(ctx, staleKey).Err(); delErr != nil {
				r.log.Error("reconciler: failed to delete stale user suspended key", zap.String("key", staleKey), zap.Error(delErr))
			} else {
				r.log.Info("reconciler: deleted stale user suspended key", zap.String("key", staleKey))
			}
		}
	}

	return nil
}
