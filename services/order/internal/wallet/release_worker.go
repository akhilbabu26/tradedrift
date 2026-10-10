package wallet

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/order/internal/repository"
)

// WalletFundsReleaser abstracts the ReleaseFunds gRPC call for the worker and testing.
type WalletFundsReleaser interface {
	ReleaseFunds(ctx context.Context, orderID string) error
}

type WalletReleaseWorker struct {
	repo     repository.WalletReleaseRepository
	wallet   WalletFundsReleaser
	logger   *zap.Logger
	interval time.Duration
}

func NewWalletReleaseWorker(
	repo repository.WalletReleaseRepository,
	wallet WalletFundsReleaser,
	logger *zap.Logger,
	interval time.Duration,
) *WalletReleaseWorker {
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &WalletReleaseWorker{
		repo:     repo,
		wallet:   wallet,
		logger:   logger,
		interval: interval,
	}
}

func (w *WalletReleaseWorker) Start(ctx context.Context) {
	w.logger.Info("Starting Wallet Release background worker", zap.Duration("interval", w.interval))
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Wallet Release worker loop stopped")
			return
		case <-ticker.C:
			w.ProcessPendingReleases(ctx)
		}
	}
}

// ProcessPendingReleases claims pending wallet releases and drains them through Wallet Service.
func (w *WalletReleaseWorker) ProcessPendingReleases(ctx context.Context) {
	releases, err := w.repo.ClaimPendingWalletReleases(ctx, 50)
	if err != nil {
		w.logger.Error("Failed to claim pending wallet releases", zap.Error(err))
		return
	}

	for _, rel := range releases {
		if w.wallet == nil {
			w.logger.Warn("Wallet client is nil; skipping release processing")
			return
		}

		err := w.wallet.ReleaseFunds(ctx, rel.OrderID)
		if err != nil {
			if IsReservationAlreadyReleased(err) {
				w.logger.Info("Reservation was already released or not found; completing release record",
					zap.String("release_id", rel.ID),
					zap.String("order_id", rel.OrderID),
				)
				if markErr := w.repo.MarkWalletReleaseCompleted(ctx, rel.ID); markErr != nil {
					w.logger.Error("Failed to mark idempotent wallet release completed", zap.Error(markErr))
				}
				continue
			}

			w.logger.Warn("Failed to release funds in Wallet Service; recording retry backoff",
				zap.String("release_id", rel.ID),
				zap.String("order_id", rel.OrderID),
				zap.Int("attempt", rel.Attempts),
				zap.Error(err),
			)
			if recErr := w.repo.RecordWalletReleaseError(ctx, rel.ID, err.Error()); recErr != nil {
				w.logger.Error("Failed to record wallet release error", zap.Error(recErr))
			}
			continue
		}

		if markErr := w.repo.MarkWalletReleaseCompleted(ctx, rel.ID); markErr != nil {
			w.logger.Error("Failed to mark wallet release as completed",
				zap.String("release_id", rel.ID),
				zap.Error(markErr),
			)
			continue
		}
		w.logger.Info("Successfully completed durable wallet release",
			zap.String("release_id", rel.ID),
			zap.String("order_id", rel.OrderID),
		)
	}
}

// IsReservationAlreadyReleased checks if an error indicates that the reservation does not exist
// or has already been settled/released, making release idempotent.
func IsReservationAlreadyReleased(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "reservation not found") ||
		strings.Contains(msg, "already released") ||
		strings.Contains(msg, "reservation does not exist") ||
		strings.Contains(msg, "already settled") ||
		strings.Contains(msg, "already completed")
}
