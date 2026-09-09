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

// marketRE matches BASE-QUOTE where each side consists of 1 to 20 uppercase alphanumeric chars.
var marketRE = regexp.MustCompile(`^[A-Z0-9]{1,20}-[A-Z0-9]{1,20}$`)

// decimalRE matches a positive decimal number with optional fraction (e.g. "100", "0.05", "1234.5678").
// Strictly rejects negatives, scientific notation, letters, and empty strings.
var decimalRE = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// validateUUID returns a descriptive error when value is not a canonical UUID.
// Both lower-case and upper-case hex digits are accepted.
func validateUUID(field, value string) error {
	if !uuidRE.MatchString(strings.ToLower(value)) {
		return fmt.Errorf("%w: field=%s value=%q", ErrInvalidObjectID, field, value)
	}
	return nil
}

// validateMarketID checks that marketID conforms to structural BASE-QUOTE format.
func validateMarketID(marketID string) error {
	if !marketRE.MatchString(marketID) {
		return fmt.Errorf("invalid market_id %q: must be in BASE-QUOTE format", marketID)
	}
	return nil
}

// validatePositiveDecimal verifies that val represents a valid positive decimal quantity or price
// without converting to float64, preserving exact decimal precision and representations.
func validatePositiveDecimal(field, val string) error {
	if !decimalRE.MatchString(val) {
		return fmt.Errorf("invalid %s %q: must be a positive decimal string", field, val)
	}
	hasNonZero := false
	for _, r := range val {
		if r >= '1' && r <= '9' {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		return fmt.Errorf("invalid %s %q: value must be greater than zero", field, val)
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

// Validate checks that all required fields are present, IDs are canonical UUIDs,
// market conforms to BASE-QUOTE, and price/quantity are strictly positive decimals.
// Returns a descriptive error suitable for DLQ routing at the consumer layer.
func (ev *TradeSettledEvent) Validate() error {
	if ev.EventID == "" || ev.TradeID == "" || ev.BuyerUserID == "" || ev.SellerUserID == "" {
		return fmt.Errorf("missing required fields (event_id, trade_id, buyer_user_id, seller_user_id)")
	}
	for _, f := range []struct{ name, val string }{
		{"event_id", ev.EventID},
		{"trade_id", ev.TradeID},
		{"buyer_user_id", ev.BuyerUserID},
		{"seller_user_id", ev.SellerUserID},
	} {
		if err := validateUUID(f.name, f.val); err != nil {
			return err
		}
	}
	// Optional UUID fields — only validate when present.
	for _, f := range []struct{ name, val string }{
		{"buy_order_id", ev.BuyOrderID},
		{"sell_order_id", ev.SellOrderID},
	} {
		if f.val != "" {
			if err := validateUUID(f.name, f.val); err != nil {
				return err
			}
		}
	}

	if err := validateMarketID(ev.MarketID); err != nil {
		return err
	}
	if err := validatePositiveDecimal("price", ev.Price); err != nil {
		return err
	}
	if err := validatePositiveDecimal("quantity", ev.Quantity); err != nil {
		return err
	}

	return nil
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

// Validate checks that all required UUID fields are present and well-formed.
func (ev *OrderCancelledEvent) Validate() error {
	if ev.EventID == "" || ev.OrderID == "" || ev.UserID == "" {
		return fmt.Errorf("missing required fields (event_id, order_id, user_id)")
	}
	for _, f := range []struct{ name, val string }{
		{"event_id", ev.EventID},
		{"order_id", ev.OrderID},
		{"user_id", ev.UserID},
	} {
		if err := validateUUID(f.name, f.val); err != nil {
			return err
		}
	}
	if ev.MarketID != "" {
		if err := validateMarketID(ev.MarketID); err != nil {
			return err
		}
	}
	return nil
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

// Validate checks that all required UUID fields are present and well-formed.
func (ev *PortfolioUpdatedEvent) Validate() error {
	if ev.EventID == "" || ev.UserID == "" {
		return fmt.Errorf("missing required fields (event_id, user_id)")
	}
	for _, f := range []struct{ name, val string }{
		{"event_id", ev.EventID},
		{"user_id", ev.UserID},
	} {
		if err := validateUUID(f.name, f.val); err != nil {
			return err
		}
	}
	return nil
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
