package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/auth/internal/repository"
)

// WalletReconciler is a background worker that ensures all newly registered
// users whose wallet initialization failed or timed out during the synchronous
// flow get their wallets reliably initialized with exponential backoff and observability.
type WalletReconciler struct {
	outboxRepo  repository.OutboxRepository
	walletCl    WalletClient
	log         *zap.Logger
	interval    time.Duration
	maxAttempts int
	wg          sync.WaitGroup
}

// NewWalletReconciler creates a new background wallet reconciler.
func NewWalletReconciler(
	outboxRepo repository.OutboxRepository,
	walletCl WalletClient,
	log *zap.Logger,
	interval time.Duration,
) *WalletReconciler {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &WalletReconciler{
		outboxRepo:  outboxRepo,
		walletCl:    walletCl,
		log:         log,
		interval:    interval,
		maxAttempts: 5,
	}
}

// Start begins the reconciliation loop, running until ctx is canceled.
func (r *WalletReconciler) Start(ctx context.Context) {
	r.wg.Add(1)
	defer r.wg.Done()

	r.log.Info("WalletReconciler started", zap.Duration("interval", r.interval))
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// Run initial reconciliation on startup
	r.reconcileBatch(ctx)

	for {
		select {
		case <-ctx.Done():
			r.log.Info("WalletReconciler stopping cleanly")
			return
		case <-ticker.C:
			r.reconcileBatch(ctx)
		}
	}
}

// Wait blocks until the reconciler goroutine has finished.
func (r *WalletReconciler) Wait() {
	r.wg.Wait()
}

// ReconcileOnce performs a single pass over pending wallet initialization events.
// Useful for synchronous triggering in tests or manual reconciliation.
func (r *WalletReconciler) ReconcileOnce(ctx context.Context) (int, error) {
	return r.reconcileBatch(ctx)
}

type eventPayloadMetadata struct {
	Attempts int `json:"attempts"`
}

func (r *WalletReconciler) reconcileBatch(ctx context.Context) (int, error) {
	if r.outboxRepo == nil || r.walletCl == nil {
		return 0, nil
	}

	fetchCtx, fetchCancel := context.WithTimeout(ctx, 5*time.Second)
	// Multi-instance replica claiming with FOR UPDATE SKIP LOCKED
	events, err := r.outboxRepo.ClaimPendingEvents(fetchCtx, "WalletInitializePending", 50)
	fetchCancel()
	if err != nil {
		// Fallback to GetPendingEvents if ClaimPendingEvents encountered an error
		var fallbackErr error
		fallbackCtx, fallbackCancel := context.WithTimeout(ctx, 5*time.Second)
		events, fallbackErr = r.outboxRepo.GetPendingEvents(fallbackCtx, "WalletInitializePending", 50)
		fallbackCancel()
		if fallbackErr != nil {
			r.log.Error("WalletReconciler: failed to claim or fetch pending events", zap.Error(err), zap.NamedError("fallbackErr", fallbackErr))
			return 0, fallbackErr
		}
	}

	if len(events) == 0 {
		return 0, nil
	}

	r.log.Info("WalletReconciler: processing claimed wallet events", zap.Int("count", len(events)))
	processed := 0

	for _, event := range events {
		select {
		case <-ctx.Done():
			return processed, ctx.Err()
		default:
		}

		userID := event.AggregateID
		if userID == "" {
			_ = r.outboxRepo.MarkProcessed(ctx, event.ID)
			continue
		}

		// Decode previous attempt count from JSON payload
		var meta eventPayloadMetadata
		if len(event.Payload) > 0 {
			_ = json.Unmarshal(event.Payload, &meta)
		}
		currentAttempt := meta.Attempts + 1

		callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
		initErr := r.walletCl.InitializeWallet(callCtx, userID)
		callCancel()

		if initErr == nil {
			if err := r.outboxRepo.MarkProcessed(ctx, event.ID); err != nil {
				r.log.Error("WalletReconciler: failed to mark event processed",
					zap.String("eventID", event.ID),
					zap.String("userID", userID),
					zap.Error(err),
				)
			} else {
				processed++
				r.log.Info("WalletReconciler: successfully initialized wallet for user",
					zap.String("userID", userID),
					zap.Int("attempt", currentAttempt),
				)
			}
		} else {
			if currentAttempt >= r.maxAttempts {
				r.log.Error("ALERT: Wallet initialization permanently failed after maximum attempts; manual intervention required",
					zap.String("eventID", event.ID),
					zap.String("userID", userID),
					zap.Int("attempts", currentAttempt),
					zap.Error(initErr),
				)
			} else {
				r.log.Warn("WalletReconciler: wallet initialization failed, scheduled exponential backoff retry",
					zap.String("eventID", event.ID),
					zap.String("userID", userID),
					zap.Int("attempt", currentAttempt),
					zap.Error(initErr),
				)
			}

			_ = r.outboxRepo.RecordFailureWithBackoff(ctx, event.ID, currentAttempt, r.maxAttempts, initErr.Error())
		}
	}

	return processed, nil
}
