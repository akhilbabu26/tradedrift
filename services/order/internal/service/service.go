package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"tradedrift/services/order/internal/repository"
	"tradedrift/services/order/internal/wallet"
)

type Service interface {
	CreateOrder(ctx context.Context, req *CreateOrderParams) (*repository.Order, error)
	CancelOrder(ctx context.Context, orderID, userID string) (*repository.Order, error)
	GetOrder(ctx context.Context, orderID, userID string) (*repository.Order, error)
	ListOrders(ctx context.Context, userID, marketID, cursor string, side repository.OrderSide, status repository.OrderStatus, fromTime, toTime *time.Time, limit int32) ([]*repository.Order, error)
}

type CreateOrderParams struct {
	UserID         string
	MarketID       string
	Side           repository.OrderSide
	OrderType      repository.OrderType
	Price          string
	Quantity       string
	IdempotencyKey string
}

type orderService struct {
	repo   repository.OrderRepository
	wallet *wallet.Client
	logger *zap.Logger
}

func NewService(repo repository.OrderRepository, walletClient *wallet.Client, logger *zap.Logger) Service {
	return &orderService{
		repo:   repo,
		wallet: walletClient,
		logger: logger,
	}
}

func (s *orderService) CreateOrder(ctx context.Context, p *CreateOrderParams) (*repository.Order, error) {
	// 1. Strict Side & Type Validation
	switch p.Side {
	case repository.SideBuy, repository.SideSell:
	default:
		return nil, ErrInvalidSide
	}

	switch p.OrderType {
	case repository.TypeLimit, repository.TypeMarket:
	default:
		return nil, ErrInvalidType
	}

	// 2. Full Idempotency Parameter Check
	if p.IdempotencyKey != "" {
		existing, err := s.repo.FindByIdempotencyKey(ctx, p.IdempotencyKey)
		if err != nil {
			return nil, fmt.Errorf("check idempotency key: %w", err)
		}
		if existing != nil {
			if sameRequest(existing, p) {
				s.logger.Info("Idempotency key hit with matching parameters, returning existing order", zap.String("order_id", existing.ID))
				return existing, nil
			}
			return nil, ErrDuplicateIdempotencyKey
		}
	}

	// 3. Parse Market ID (Canonical BASE-QUOTE format: "BTC-USDT")
	baseAsset, quoteAsset, err := parseMarketID(p.MarketID)
	if err != nil {
		return nil, ErrInvalidMarket
	}

	// 4. Decimal Financial Validation: Quantity ALWAYS represents base asset quantity
	qty, err := decimal.NewFromString(p.Quantity)
	if err != nil || !qty.GreaterThan(decimal.Zero) {
		return nil, ErrInvalidQuantity
	}

	var priceDec decimal.Decimal
	if p.OrderType == repository.TypeLimit || p.Side == repository.SideBuy {
		// LIMIT orders and MARKET BUY orders require a price (limit price or max price cap to determine quote reservation)
		if p.Price == "" {
			return nil, ErrInvalidPrice
		}
		priceDec, err = decimal.NewFromString(p.Price)
		if err != nil || !priceDec.GreaterThan(decimal.Zero) {
			return nil, ErrInvalidPrice
		}
	} else {
		// MARKET SELL orders omit price
		if p.Price != "" {
			priceDec, err = decimal.NewFromString(p.Price)
			if err != nil || !priceDec.GreaterThan(decimal.Zero) {
				return nil, ErrInvalidPrice
			}
		}
	}

	// 5. Calculate Exact Reservation Amount & Asset
	var reserveAsset, reserveAmount string
	if p.Side == repository.SideBuy {
		// BUY Order: reserve quote asset (Price * Quantity)
		reserveAsset = quoteAsset
		totalQuote := priceDec.Mul(qty)
		reserveAmount = totalQuote.StringFixed(10)
	} else {
		// SELL Order: reserve base asset (Quantity)
		reserveAsset = baseAsset
		reserveAmount = qty.StringFixed(10)
	}

	// 6. Generate UUIDv7 Order ID
	orderID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate order UUIDv7: %w", err)
	}

	// 7. Construct Order Struct & Serialize Payload BEFORE Network Calls
	now := time.Now().UTC()
	var pricePtr *string
	if !priceDec.IsZero() {
		formattedPrice := priceDec.StringFixed(10)
		pricePtr = &formattedPrice
	}

	var keyPtr *string
	if p.IdempotencyKey != "" {
		keyPtr = &p.IdempotencyKey
	}

	formattedQty := qty.StringFixed(10)
	order := &repository.Order{
		ID:                orderID.String(),
		UserID:            p.UserID,
		MarketID:          p.MarketID,
		Side:              p.Side,
		OrderType:         p.OrderType,
		Price:             pricePtr,
		Quantity:          formattedQty,
		FilledQuantity:    "0.0000000000",
		RemainingQuantity: formattedQty,
		Status:            repository.StatusOpen,
		IdempotencyKey:    keyPtr,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	const mmAccountUUID = "00000000-0000-0000-0000-000000000001"
	isMMAccount := (p.UserID == mmAccountUUID)

	var envelopeBytes []byte
	if !isMMAccount {
		// Normal user orders: generate outbox envelope for Kafka publishing
		orderPayloadBytes, err := json.Marshal(map[string]any{
			"order_id":   order.ID,
			"user_id":    order.UserID,
			"market_id":  order.MarketID,
			"side":       order.Side,
			"order_type": order.OrderType,
			"price":      order.Price,
			"quantity":   order.Quantity,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal order payload: %w", err)
		}

		envelopeBytes, err = json.Marshal(map[string]any{
			"event_id":      uuid.NewString(),
			"event_type":    "OrderCreated",
			"event_version": 1,
			"market_id":     order.MarketID,
			"occurred_at":   now.UTC(),
			"payload":       json.RawMessage(orderPayloadBytes),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal outbox envelope: %w", err)
		}
	} else {
		// SYSTEM MM INVARIANT:
		// Order Service acts purely as the authoritative recovery database for MM orders.
		// The Liquidity Engine holds exclusive authority to publish OrderCreated commands
		// to Kafka. Enqueuing outbox events for MM orders would cause duplicate Kafka emissions.
		s.logger.Info("MM order registration: skipping outbox enqueue (LE is sole Kafka publisher)",
			zap.String("order_id", orderID.String()),
			zap.String("user_id", p.UserID))
	}

	// 8. Reserve Funds in Wallet Service (Network Call) — Skipped for MM account
	if !isMMAccount {
		s.logger.Info("Reserving funds in Wallet Service",
			zap.String("order_id", orderID.String()),
			zap.String("asset", reserveAsset),
			zap.String("amount", reserveAmount),
		)
		_, _, err = s.wallet.ReserveFunds(ctx, p.UserID, orderID.String(), reserveAsset, reserveAmount)
		if err != nil {
			st, ok := status.FromError(err)
			if ok && st.Code() == codes.FailedPrecondition {
				return nil, ErrInsufficientFunds
			}
			s.logger.Error("Wallet ReserveFunds gRPC transport failure", zap.Error(err))
			return nil, fmt.Errorf("wallet service reservation error: %w", err)
		}
	} else {
		s.logger.Info("MM order creation: skipping wallet ReserveFunds (inventory tracked locally in LE)",
			zap.String("order_id", orderID.String()),
			zap.String("user_id", p.UserID))
	}

	// 9. Persist Order + Outbox Atomically (With SAGA ReleaseFunds Compensation on DB Failure for user orders)
	if err := s.repo.CreateOrder(ctx, order, envelopeBytes); err != nil {
		s.logger.Error("PostgreSQL CreateOrder failed post-fund reservation. Triggering Saga ReleaseFunds compensation",
			zap.String("order_id", orderID.String()),
			zap.Error(err),
		)
		if !isMMAccount {
			if releaseErr := s.wallet.ReleaseFunds(ctx, orderID.String()); releaseErr != nil {
				s.logger.Error("SAGA CRITICAL FAILURE: Compensating ReleaseFunds failed!",
					zap.String("order_id", orderID.String()),
					zap.Error(releaseErr),
				)
			}
		}
		if errors.Is(err, repository.ErrDuplicateIdempotencyKey) {
			return nil, ErrDuplicateIdempotencyKey
		}
		return nil, fmt.Errorf("persist order failed: %w", err)
	}

	s.logger.Info("Order created successfully", zap.String("order_id", order.ID))
	return order, nil
}

func (s *orderService) CancelOrder(ctx context.Context, orderID, userID string) (*repository.Order, error) {
	order, err := s.repo.GetByID(ctx, orderID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for cancel: %w", err)
	}

	if order.Status != repository.StatusOpen && order.Status != repository.StatusPartiallyFilled {
		return nil, ErrOrderNotCancellable
	}

	cancelPayloadBytes, err := json.Marshal(map[string]any{
		"order_id": order.ID,
		"user_id":  order.UserID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal cancel payload: %w", err)
	}

	envelopeBytes, err := json.Marshal(map[string]any{
		"event_id":      uuid.NewString(),
		"event_type":    "OrderCancelRequested",
		"event_version": 1,
		"market_id":     order.MarketID,
		"occurred_at":   time.Now().UTC(),
		"payload":       json.RawMessage(cancelPayloadBytes),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal cancel envelope: %w", err)
	}

	if err := s.repo.UpdateStatusToCancelling(ctx, order, envelopeBytes); err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, ErrOrderNotFound
		}
		if errors.Is(err, repository.ErrOrderNotCancellable) {
			return nil, ErrOrderNotCancellable
		}
		return nil, fmt.Errorf("update order status: %w", err)
	}

	order.Status = repository.StatusCancelling
	return order, nil
}

func (s *orderService) GetOrder(ctx context.Context, orderID, userID string) (*repository.Order, error) {
	order, err := s.repo.GetByID(ctx, orderID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order: %w", err)
	}
	return order, nil
}

func (s *orderService) ListOrders(ctx context.Context, userID, marketID, cursor string, side repository.OrderSide, status repository.OrderStatus, fromTime, toTime *time.Time, limit int32) ([]*repository.Order, error) {
	orders, err := s.repo.ListOrders(ctx, userID, marketID, cursor, side, status, fromTime, toTime, limit)
	if err != nil {
		if errors.Is(err, repository.ErrInvalidPaginationCursor) {
			return nil, ErrInvalidPaginationCursor
		}
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}
