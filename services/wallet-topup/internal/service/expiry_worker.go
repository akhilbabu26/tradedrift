package service

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/wallet-topup/internal/repository"
)

type ExpiryWorker struct {
	orderRepo repository.TopUpOrderRepository
	txManager repository.TransactionManager
	log       *zap.Logger
	interval  time.Duration
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func NewExpiryWorker(
	orderRepo repository.TopUpOrderRepository,
	txManager repository.TransactionManager,
	log *zap.Logger,
	interval time.Duration,
) *ExpiryWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &ExpiryWorker{
		orderRepo: orderRepo,
		txManager: txManager,
		log:       log,
		interval:  interval,
		stopCh:    make(chan struct{}),
	}
}

func (w *ExpiryWorker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		w.log.Info("ExpiryWorker started", zap.Duration("interval", w.interval))

		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-ticker.C:
				w.sweepExpired(ctx)
			}
		}
	}()
}

func (w *ExpiryWorker) Stop() {
	close(w.stopCh)
	w.wg.Wait()
	w.log.Info("ExpiryWorker stopped")
}

func (w *ExpiryWorker) sweepExpired(ctx context.Context) {
	now := time.Now().UTC()
	expiredOrders, err := w.orderRepo.GetPendingExpired(ctx, now, 100)
	if err != nil {
		w.log.Error("ExpiryWorker: failed to query expired orders", zap.Error(err))
		return
	}

	for _, order := range expiredOrders {
		// Atomic single transaction: transitions order to EXPIRED and decrements reserved_inr
		if err := w.txManager.ExpireOrderAndReleaseQuotaTx(ctx, order.ID, order.UserID, order.ReservationDate, order.INRAmount); err != nil {
			w.log.Error("ExpiryWorker: failed to atomically expire order and release quota",
				zap.String("orderID", order.ID),
				zap.String("userID", order.UserID),
				zap.Error(err),
			)
			continue
		}

		w.log.Info("ExpiryWorker: atomically expired order and released reserved quota",
			zap.String("orderID", order.ID),
			zap.String("userID", order.UserID),
			zap.Int64("releasedINR", order.INRAmount),
		)
	}
}
