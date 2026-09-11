package service

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/payment"
	"tradedrift/services/wallet-topup/internal/repository"
)

type TopUpService struct {
	dailyLimitRepo  repository.DailyLimitRepository
	orderRepo       repository.TopUpOrderRepository
	txManager       repository.TransactionManager
	paymentProvider payment.PaymentProvider
	dailyLimitINR   int64
	log             *zap.Logger
	locIST          *time.Location
}

func NewTopUpService(
	dailyLimitRepo repository.DailyLimitRepository,
	orderRepo repository.TopUpOrderRepository,
	txManager repository.TransactionManager,
	paymentProvider payment.PaymentProvider,
	dailyLimitINR int64,
	log *zap.Logger,
) *TopUpService {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.FixedZone("IST", 5*3600+30*60)
	}
	if dailyLimitINR <= 0 {
		dailyLimitINR = 10
	}
	return &TopUpService{
		dailyLimitRepo:  dailyLimitRepo,
		orderRepo:       orderRepo,
		txManager:       txManager,
		paymentProvider: paymentProvider,
		dailyLimitINR:   dailyLimitINR,
		log:             log,
		locIST:          loc,
	}
}

// CreateTopUp initiates a fiat top-up order:
//  1. Validates whole-rupee amount (₹1..₹10).
//  2. Atomically pre-reserves idempotency key and quota inside InitiateTopUpTx (INITIATED state).
//     If two concurrent requests arrive with the same key, exactly one acquires the key, preventing duplicate provider orders.
//  3. Calls external PaymentProvider with deterministic internal orderID as merchant reference.
//  4. If provider succeeds: transitions order to PAYMENT_PENDING.
//  5. If provider fails: cancels initiated order and releases quota inside atomic transaction.
func (s *TopUpService) CreateTopUp(ctx context.Context, userID, idempotencyKey string, inrAmount int64) (*domain.TopUpOrder, error) {
	if userID == "" {
		return nil, domain.ErrUnauthorized
	}
	if idempotencyKey == "" {
		return nil, domain.ErrMissingIdempotency
	}
	if len(idempotencyKey) > 100 {
		return nil, domain.ErrInvalidIdempotencyKey
	}
	if inrAmount < 1 || inrAmount > 10 {
		return nil, domain.ErrInvalidAmount
	}

	orderID, err := platformuuid.New()
	if err != nil {
		return nil, fmt.Errorf("failed to generate order UUID: %w", err)
	}

	nowIST := time.Now().In(s.locIST)
	usageDate := nowIST.Format("2006-01-02")
	nowUTC := time.Now().UTC()
	usdtAmount := fmt.Sprintf("%d.0000000000", inrAmount*1000)

	candidateOrder := &domain.TopUpOrder{
		ID:              orderID,
		UserID:          userID,
		IdempotencyKey:  idempotencyKey,
		INRAmount:       inrAmount,
		USDTAmount:      usdtAmount,
		ReservationDate: usageDate,
		Provider:        s.paymentProvider.ProviderName(),
		Status:          domain.StatusInitiated,
		AttemptCount:    0,
		ExpiresAt:       nowUTC.Add(15 * time.Minute),
		CreatedAt:       nowUTC,
		UpdatedAt:       nowUTC,
	}

	// ── 1. Atomic Pre-Reservation & Idempotency Check in Single DB Transaction ─
	existing, err := s.txManager.InitiateTopUpTx(ctx, candidateOrder, s.dailyLimitINR)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		s.log.Info("CreateTopUp: returning existing idempotent order",
			zap.String("orderID", existing.ID),
			zap.String("idempotencyKey", idempotencyKey),
			zap.Int64("inrAmount", existing.INRAmount),
		)
		return existing, nil
	}

	// ── 2. Create External Provider Order Using Deterministic Internal Order ID ──
	provRes, err := s.paymentProvider.CreateOrder(ctx, orderID, inrAmount, "INR")
	if err != nil {
		// Rollback initiated order and release reserved quota atomically
		cancelErr := s.txManager.CancelInitiatedOrderTx(ctx, orderID, userID, usageDate, inrAmount, err.Error())
		if cancelErr != nil {
			// CRITICAL OPERATIONAL ALERT: Quota rollback failed!
			s.log.Error("CRITICAL OPERATIONAL ALERT: failed to cancel initiated order and release quota after provider failure",
				zap.String("orderID", orderID),
				zap.String("userID", userID),
				zap.Int64("amountINR", inrAmount),
				zap.Error(cancelErr),
			)
		}
		return nil, fmt.Errorf("payment gateway order creation failed: %w", err)
	}

	// ── 3. Activate Order to PAYMENT_PENDING ───────────────────────────────────
	if err := s.txManager.ActivatePaymentPending(ctx, orderID, provRes.ProviderOrderID); err != nil {
		s.log.Error("CreateTopUp: failed to activate payment pending status",
			zap.String("orderID", orderID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to activate payment pending: %w", err)
	}

	candidateOrder.Status = domain.StatusPaymentPending
	candidateOrder.ProviderOrderID = &provRes.ProviderOrderID

	s.log.Info("CreateTopUp: order created and activated successfully",
		zap.String("orderID", orderID),
		zap.String("userID", userID),
		zap.Int64("inrAmount", inrAmount),
		zap.String("usdtAmount", usdtAmount),
		zap.String("providerOrderID", provRes.ProviderOrderID),
	)

	return candidateOrder, nil
}

// GetDailyUsage returns the user's daily quota metrics and reset time.
func (s *TopUpService) GetDailyUsage(ctx context.Context, userID string) (*domain.DailyUsageResponse, error) {
	if userID == "" {
		return nil, domain.ErrUnauthorized
	}

	nowIST := time.Now().In(s.locIST)
	usageDate := nowIST.Format("2006-01-02")

	limit, err := s.dailyLimitRepo.GetDailyUsage(ctx, userID, usageDate, s.dailyLimitINR)
	if err != nil {
		return nil, fmt.Errorf("failed to get daily usage: %w", err)
	}

	tomorrowIST := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day()+1, 0, 0, 0, 0, s.locIST)

	return &domain.DailyUsageResponse{
		UserID:       userID,
		UsageDate:    usageDate,
		LimitINR:     limit.LimitINR,
		ReservedINR:  limit.ReservedINR,
		ConsumedINR:  limit.ConsumedINR,
		RemainingINR: limit.RemainingINR(),
		ResetsAt:     tomorrowIST.Format(time.RFC3339),
	}, nil
}

// GetTopUpByID retrieves order status for a user.
func (s *TopUpService) GetTopUpByID(ctx context.Context, userID, orderID string) (*domain.TopUpOrder, error) {
	if userID == "" {
		return nil, domain.ErrUnauthorized
	}
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.UserID != userID {
		return nil, domain.ErrOrderNotFound
	}
	return order, nil
}
