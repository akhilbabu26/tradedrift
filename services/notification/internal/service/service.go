package service

import (
	"errors"

	"go.uber.org/zap"

	"tradedrift/services/notification/internal/repository"
)

// Sentinel errors used by the inbox handlers and validated upstream by the gRPC layer.
var (
	ErrInvalidUserID = errors.New("invalid user_id: must be a valid UUID")
	ErrEmptyTitle    = errors.New("notification title cannot be empty")
	ErrEmptyMessage  = errors.New("notification message cannot be empty")
)

// ---------------------------------------------------------------------------
// Inbound domain event payloads consumed from Kafka
// Handlers are in events.go
// ---------------------------------------------------------------------------

// TradeSettledEvent is the payload produced by the Matching Engine on trades.settled.v1.
// Counterparty privacy: buyer_user_id and seller_user_id are present here but each
// generated notification exposes only the side relevant to its recipient.
type TradeSettledEvent struct {
	TradeID      string `json:"trade_id"`
	MarketID     string `json:"market_id"`
	BaseAsset    string `json:"base_asset"`
	QuoteAsset   string `json:"quote_asset"`
	BuyerUserID  string `json:"buyer_user_id"`
	SellerUserID string `json:"seller_user_id"`
	BuyOrderID   string `json:"buy_order_id"`
	SellOrderID  string `json:"sell_order_id"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	Sequence     uint64 `json:"sequence"`
	ExecutedAt   string `json:"executed_at"`
}

// OrderCancelledEvent is the payload produced on orders.cancelled.v1.
type OrderCancelledEvent struct {
	EventID   string `json:"event_id"`
	OrderID   string `json:"order_id"`
	UserID    string `json:"user_id"`
	MarketID  string `json:"market_id"`
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

// PortfolioUpdatedEvent is the payload produced on portfolios.updated.v1.
// These events stage ephemeral outbox rows for real-time streaming to the Gateway;
// they do NOT create persistent notification rows.
type PortfolioUpdatedEvent struct {
	EventID       string `json:"event_id"`
	UserID        string `json:"user_id"`
	TotalValue    string `json:"total_value"`
	RealizedPnL   string `json:"realized_pnl"`
	UnrealizedPnL string `json:"unrealized_pnl"`
	CashBalance   string `json:"cash_balance"`
	UpdatedAt     string `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Service — shared struct used by events.go and inbox.go
// ---------------------------------------------------------------------------

// Service orchestrates Kafka event ingestion and inbox management.
// It writes to PostgreSQL atomically and relies on the Transactional Outbox
// publisher to forward notifications to Redis Pub/Sub.
type Service struct {
	repo repository.NotificationRepository
	log  *zap.Logger
}

// NewService constructs the Service with its required dependencies.
func NewService(repo repository.NotificationRepository, log *zap.Logger) *Service {
	return &Service{
		repo: repo,
		log:  log,
	}
}
