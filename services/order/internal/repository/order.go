package repository

import (
	"context"
	"errors"
	"time"
)

// Sentinel Errors
var (
	ErrOrderNotFound           = errors.New("order not found")
	ErrDuplicateIdempotencyKey = errors.New("idempotency key already exists")
)

type OrderSide string
type OrderType string
type OrderStatus string

const (
	SideBuy OrderSide = "BUY"
	SideSell OrderSide = "SELL"

	TypeLimit OrderType = "LIMIT"
	TypeMarket OrderType = "MARKET"

	StatusOpen            OrderStatus = "OPEN"
	StatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	StatusFilled          OrderStatus = "FILLED"
	StatusCancelling      OrderStatus = "CANCELLING"
	StatusCancelled       OrderStatus = "CANCELLED"
)

// Order represents a user's trading order record.
type Order struct {
	ID                string
	UserID            string
	MarketID          string
	Side              OrderSide
	OrderType         OrderType
	Price             *string // nil for MARKET orders
	Quantity          string
	FilledQuantity    string
	RemainingQuantity string
	Status            OrderStatus
	IdempotencyKey    *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// OrderRepository defines the database operations for Order Service.
type OrderRepository interface {
	// FindByIdempotencyKey looks up an order by client-supplied idempotency key.
	FindByIdempotencyKey(ctx context.Context, key string) (*Order, error)
	// CreateOrder inserts the order and outbox record atomically in one transaction.
	CreateOrder(ctx context.Context, o *Order, outboxPayload []byte) error
	// GetByID retrieves a single order by order ID and user ID.
	GetByID(ctx context.Context, orderID, userID string) (*Order, error)
	// UpdateStatusToCancelling marks order status as CANCELLING and adds Outbox event.
	UpdateStatusToCancelling(ctx context.Context, o *Order, outboxPayload []byte) error
	// ListOrders returns a paginated list of orders matching filters.
	ListOrders(ctx context.Context, userID, marketID, cursor string, side OrderSide, status OrderStatus, limit int32) ([]*Order, error)
}