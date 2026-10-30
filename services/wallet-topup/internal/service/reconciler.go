package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet-topup/internal/client"
	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/payment"
	"tradedrift/services/wallet-topup/internal/repository"
)

type ReconcilerWorker struct {
	orderRepo       repository.TopUpOrderRepository
	walletClient    *client.WalletClient
	log             *zap.Logger
	interval        time.Duration
	batchSize       int
	leaseDuration   time.Duration
	stopCh          chan struct{}
	triggerCh       chan struct{}
	txManager       repository.TransactionManager
	paymentProvider payment.PaymentProvider
	dailyLimitINR   int64
	wg              sync.WaitGroup
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
		triggerCh:     make(chan struct{}, 1),
	}
}

// SetProviderReconciler configures provider lookups to automatically unstrand stuck payments.
func (w *ReconcilerWorker) SetProviderReconciler(txManager repository.TransactionManager, provider payment.PaymentProvider, dailyLimitINR int64) {
	w.txManager = txManager
	w.paymentProvider = provider
	w.dailyLimitINR = dailyLimitINR
}

// Trigger notifies the reconciler worker to process a batch immediately without waiting for the next ticker tick.
func (w *ReconcilerWorker) Trigger() {
	select {
	case w.triggerCh <- struct{}{}:
	default:
	}
}

func (w *ReconcilerWorker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		stuckTicker := time.NewTicker(30 * time.Second)
		defer stuckTicker.Stop()

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
			case <-w.triggerCh:
				w.processBatch(ctx)
			case <-stuckTicker.C:
				w.reconcileStuckPayments(ctx)
			}
		}
	}()
}

func (w *ReconcilerWorker) Stop() {
	close(w.stopCh)
	w.wg.Wait()
	w.log.Info("ReconcilerWorker stopped")
}

// computeBackoff calculates bounded exponential backoff with jitter (2s * 2^(attempt-1), capped at 60s).
func computeBackoff(attemptCount int) time.Duration {
	if attemptCount <= 0 {
		attemptCount = 1
	}
	baseMs := int64(2000)
	shift := attemptCount - 1
	if shift > 5 {
		shift = 5 // max 2^5 * 2000ms = 64s, capped at 60s
	}
	backoffMs := baseMs * (1 << shift)
	if backoffMs > 60000 {
		backoffMs = 60000
	}
	// Add 0-25% jitter
	jitter := rand.Int63n(backoffMs/4 + 1)
	return time.Duration(backoffMs+jitter) * time.Millisecond
}

// reconcileStuckPayments checks provider server-side APIs for unconfirmed PAYMENT_PENDING orders older than 2 minutes.
func (w *ReconcilerWorker) reconcileStuckPayments(ctx context.Context) {
	if w.txManager == nil || w.paymentProvider == nil {
		return
	}

	stuckOrders, err := w.orderRepo.GetPendingUnconfirmed(ctx, 2*time.Minute, 10)
	if err != nil {
		w.log.Warn("ReconcilerWorker: failed to query stuck unconfirmed orders", zap.Error(err))
		return
	}
	if len(stuckOrders) == 0 {
		return
	}

	for _, o := range stuckOrders {
		if o.ProviderOrderID == nil || *o.ProviderOrderID == "" {
			continue
		}

		payments, err := w.paymentProvider.FetchOrderPayments(ctx, *o.ProviderOrderID)
		if err != nil {
			w.log.Debug("ReconcilerWorker: could not fetch order payments from provider",
				zap.String("orderID", o.ID),
				zap.String("providerOrderID", *o.ProviderOrderID),
				zap.Error(err),
			)
			continue
		}

		for _, p := range payments {
			if (p.Status == "captured" || p.Captured) && p.AmountINR == o.INRAmount {
				w.log.Info("ReconcilerWorker: found captured payment for stranded order, reconciling",
					zap.String("orderID", o.ID),
					zap.String("paymentID", p.PaymentID),
					zap.Int64("amountINR", p.AmountINR),
				)

				captureTime := time.Now().UTC()
				if p.CreatedAt > 0 {
					captureTime = time.Unix(p.CreatedAt, 0).UTC()
				}
				paidDate := captureTime.Format("2006-01-02")

				payloadBytes, _ := json.Marshal(map[string]any{
					"source":            "reconciler_provider_sync",
					"order_id":          o.ID,
					"provider_order_id": *o.ProviderOrderID,
					"payment_id":        p.PaymentID,
					"reconciled_at":     time.Now().UTC().Format(time.RFC3339),
				})

				res, txErr := w.txManager.ProcessPaymentConfirmationTx(
					ctx,
					o.ID,
					o.Provider,
					p.PaymentID,
					paidDate,
					payloadBytes,
					fmt.Sprintf("reconciler_sync_%s", p.PaymentID),
					w.dailyLimitINR,
				)
				if txErr != nil {
					w.log.Error("ReconcilerWorker: failed to confirm stranded payment",
						zap.String("orderID", o.ID),
						zap.Error(txErr),
					)
				} else if res != nil {
					w.log.Info("ReconcilerWorker: stranded payment successfully advanced to CREDIT_PENDING",
						zap.String("orderID", o.ID),
						zap.Bool("alreadyProcessed", res.AlreadyProcessed),
					)
					w.Trigger()
				}
				break
			}
		}
	}
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
				backoff := computeBackoff(o.AttemptCount)
				w.log.Warn("ReconcilerWorker: DepositFunds RPC failed, backing off",
					zap.String("orderID", o.ID),
					zap.String("userID", o.UserID),
					zap.Int("attemptCount", o.AttemptCount),
					zap.Duration("backoff", backoff),
					zap.Error(err),
				)
				if o.AttemptCount >= 10 {
					w.log.Error("CRITICAL OPERATIONAL ALERT: TopUp order deposit failing repeatedly",
						zap.String("orderID", o.ID),
						zap.String("userID", o.UserID),
						zap.Int("attemptCount", o.AttemptCount),
						zap.String("lastError", errMsg),
					)
				}
				// Step 3a: Record failure with lease-fencing protection and exponential backoff
				recorded, recErr := w.orderRepo.RecordClaimFailure(ctx, o.ID, workerToken, errMsg, backoff)
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
