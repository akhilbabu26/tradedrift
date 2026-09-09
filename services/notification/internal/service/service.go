package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"tradedrift/services/notification/internal/repository"
)


// Sentinel errors
var (
	ErrInvalidUserID     = errors.New("invalid user_id: must be a valid UUID")
	ErrInvalidEventID    = errors.New("invalid event_id: must be a valid UUID")
	ErrInvalidObjectID   = errors.New("invalid id: must be a valid UUID")
	ErrEmptyTitle        = errors.New("notification title cannot be empty")
	ErrEmptyMessage      = errors.New("notification message cannot be empty")
)

// uuidRE matches canonical UUID format (8-4-4-4-12 hex, case-insensitive via strings.ToLower).
// It does NOT enforce a specific UUID version or variant.
var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// validateUUID returns a descriptive error when value is not a canonical UUID.
// Both lower-case and upper-case hex digits are accepted.
func validateUUID(field, value string) error {
	if !uuidRE.MatchString(strings.ToLower(value)) {
		return fmt.Errorf("%w: field=%s value=%q", ErrInvalidObjectID, field, value)
	}
	return nil
}


// ---------------------------------------------------------------------------
// Inbound domain event payloads consumed from Kafka
// Handlers are in events.go
// ---------------------------------------------------------------------------

// TradeSettledEvent is the payload produced by the Matching Engine on trades.settled.v1.
//
// Deduplication identity:
//
//	event_id  = source Kafka/domain event ID (unique per trade-settled emission)
//	trade_id  = the trade being settled (may be referenced by many events in theory)
//
// One TradeSettled source event creates two notifications:
//
//	Buyer  notification — notification_id = N1
//	Seller notification — notification_id = N2
//
// Counterparty privacy: buyer_user_id and seller_user_id are present in this struct
// but each generated notification exposes only the side relevant to its recipient.
type TradeSettledEvent struct {
	EventID      string `json:"event_id"`      // Unique domain event identifier for deduplication
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
