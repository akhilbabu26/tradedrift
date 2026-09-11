package service

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet-topup/internal/client"
	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/repository"
)

type ReconcilerWorker struct {
	orderRepo     repository.TopUpOrderRepository
	walletClient  *client.WalletClient
	log           *zap.Logger
	interval      time.Duration
	batchSize     int
	leaseDuration time.Duration
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

func NewReconcilerWorker(
	orderRepo repository.TopUpOrderRepository,
	walletClient *client.WalletClient,
	log *zap.Logger,
	interval time.Duration,
	batchSize int,
	leaseDuration time.Duration,
) *ReconcilerWorker {
	if interval <= 0 {
		interval = 1 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 10
	}
	if leaseDuration <= 0 {
		leaseDuration = 60 * time.Second
	}
	return &ReconcilerWorker{
		orderRepo:     orderRepo,
		walletClient:  walletClient,
		log:           log,
		interval:      interval,
		batchSize:     batchSize,
		leaseDuration: leaseDuration,
		stopCh:        make(chan struct{}),
	}
}

func (w *ReconcilerWorker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		w.log.Info("ReconcilerWorker started",
			zap.Duration("interval", w.interval),
			zap.Int("batchSize", w.batchSize),
			zap.Duration("leaseDuration", w.leaseDuration),
		)

		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-ticker.C:
				w.processBatch(ctx)
			}
		}
	}()
}

func (w *ReconcilerWorker) Stop() {
	close(w.stopCh)
	w.wg.Wait()
	w.log.Info("ReconcilerWorker stopped")
}

func (w *ReconcilerWorker) processBatch(ctx context.Context) {
	workerToken, err := platformuuid.New()
	if err != nil {
		w.log.Error("ReconcilerWorker: failed to generate worker lease token", zap.Error(err))
		return
	}

	// ── Step 1: Claim batch inside short transaction (FOR UPDATE SKIP LOCKED) ─
	claimedOrders, err := w.orderRepo.ClaimBatchForCredit(ctx, workerToken, w.batchSize, w.leaseDuration)
	if err != nil {
		w.log.Error("ReconcilerWorker: failed to claim batch for credit", zap.Error(err))
		return
	}

	if len(claimedOrders) == 0 {
		return
	}

	w.log.Info("ReconcilerWorker: claimed orders for processing",
		zap.Int("count", len(claimedOrders)),
		zap.String("workerToken", workerToken),
	)

	// ── Step 2: Call WalletService.DepositFunds with bounded concurrency pool ─
	var batchWg sync.WaitGroup
	sem := make(chan struct{}, 5) // max 5 concurrent deposit RPCs

	for _, order := range claimedOrders {
		batchWg.Add(1)
		sem <- struct{}{}

		go func(o *domain.TopUpOrder) {
			defer batchWg.Done()
			defer func() { <-sem }()

			callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			res, err := w.walletClient.DepositFunds(callCtx,
				o.UserID,
				"USDT",
				o.USDTAmount,
				o.ID,
				"TOPUP",
			)
			cancel()

			if err != nil || res == nil || !res.Success {
				errMsg := "deposit failed"
				if err != nil {
					errMsg = err.Error()
				} else if res == nil {
					errMsg = "wallet service returned nil response"
				} else if !res.Success {
					errMsg = "wallet service returned unsuccessful status"
				}
				w.log.Warn("ReconcilerWorker: DepositFunds RPC failed",
					zap.String("orderID", o.ID),
					zap.String("userID", o.UserID),
					zap.Error(err),
				)
				// Step 3a: Record failure with lease-fencing protection
				recorded, recErr := w.orderRepo.RecordClaimFailure(ctx, o.ID, workerToken, errMsg)
				if recErr != nil {
					w.log.Error("ReconcilerWorker: failed to record claim failure", zap.Error(recErr))
				} else if !recorded {
					w.log.Warn("ReconcilerWorker: lease expired while processing, failure not recorded",
						zap.String("orderID", o.ID),
						zap.String("workerToken", workerToken),
					)
				}
				return
			}

			// ── Step 3b: Complete order with lease token protection ──────────────────
			completed, compErr := w.orderRepo.CompleteOrder(ctx, o.ID, workerToken)
			if compErr != nil {
				w.log.Error("ReconcilerWorker: failed to mark order completed",
					zap.String("orderID", o.ID),
					zap.Error(compErr),
				)
			} else if !completed {
				w.log.Warn("ReconcilerWorker: lease expired or worker fenced out, completion rejected",
					zap.String("orderID", o.ID),
					zap.String("workerToken", workerToken),
				)
			} else {
				w.log.Info("ReconcilerWorker: order completed successfully",
					zap.String("orderID", o.ID),
					zap.String("newBalance", res.NewBalance),
					zap.String("walletTxnID", res.TransactionID),
				)
			}
		}(order)
	}

	batchWg.Wait()
}
