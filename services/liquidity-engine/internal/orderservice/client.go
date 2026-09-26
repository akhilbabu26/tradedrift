// Package orderservice provides a read-only gRPC client for the Order Service.
// The LE uses this for:
//   1. Startup discovery: ListMMOrders to populate the tracker
//   2. PENDING timeout check: GetOrderByClientID to confirm order state
//   3. CANCELLING timeout check: GetOrderByClientID to confirm cancel
//   4. STALE resolution: triggered via full resync (ListMMOrders again)
//
// The LE NEVER writes to the Order Service (no CreateOrder, no CancelOrder via gRPC).
// Orders are created/cancelled via Kafka commands only (orders.commands topic).
//
// IMPORTANT: The Order Service stores user_id as UUID in the DB.
// All ListOrders queries must use account.WalletUUIDStr, NOT the string "MM-001".
// The string "MM-001" is only used in Kafka command payloads (OrderCreated.user_id),
// where the Matching Engine accepts it as a plain string identifier.
package orderservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	orderv1 "tradedrift/platform/api/gen/order/v1"
	"tradedrift/services/liquidity-engine/internal/account"
	"tradedrift/services/liquidity-engine/internal/order"
)

// ErrOrderNotFound is returned when an order with the given client_order_id
// does not exist in the Order Service.
var ErrOrderNotFound = errors.New("order not found")

// OrderState is the LE's view of an order's current state from the Order Service.
type OrderState struct {
	OrderID       string          // OS-assigned UUID
	ClientOrderID string          // idempotency_key = LE's client_order_id
	Status        string          // "OPEN" | "PARTIALLY_FILLED" | "FILLED" | "CANCELLING" | "CANCELLED"
	RemainingQty  decimal.Decimal // from order.remaining_quantity
	OriginalQty   decimal.Decimal // from order.quantity
}

// Client is a thin wrapper around the Order Service gRPC client.
// All methods are read-only from the LE's perspective.
type Client struct {
	conn   *grpc.ClientConn
	client orderv1.OrderServiceClient
	logger *zap.Logger
}

// NewClient dials the Order Service and returns a ready client.
func NewClient(addr string, logger *zap.Logger) (*Client, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial Order Service at %s: %w", addr, err)
	}
	return &Client{
		conn:   conn,
		client: orderv1.NewOrderServiceClient(conn),
		logger: logger,
	}, nil
}

// Close closes the gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// CreateMMOrder registers an MM limit order in the Order Service for persistence and recovery.
// Uses account.WalletUUIDStr as UserId, and clientOrderID as IdempotencyKey.
func (c *Client) CreateMMOrder(ctx context.Context, marketID, side, price, quantity, clientOrderID string) (*OrderState, error) {
	protoSide := orderv1.OrderSide_ORDER_SIDE_BUY
	if side == "SELL" {
		protoSide = orderv1.OrderSide_ORDER_SIDE_SELL
	}

	resp, err := c.client.CreateOrder(ctx, &orderv1.CreateOrderRequest{
		UserId:         account.WalletUUIDStr,
		MarketId:       marketID,
		Side:           protoSide,
		OrderType:      orderv1.OrderType_ORDER_TYPE_LIMIT,
		Price:          price,
		Quantity:       quantity,
		IdempotencyKey: clientOrderID,
	})
	if err != nil {
		return nil, fmt.Errorf("create MM order in Order Service: %w", err)
	}

	o := resp.Order
	origQty, _ := decimal.NewFromString(o.Quantity)
	remQty, _ := decimal.NewFromString(o.RemainingQuantity)

	return &OrderState{
		OrderID:       o.Id,
		ClientOrderID: clientOrderID,
		Status:        protoStatusToString(o.Status),
		OriginalQty:   origQty,
		RemainingQty:  remQty,
	}, nil
}

// CancelMMOrder cancels an MM order in the Order Service via gRPC.
// Transitions the order to CANCELLING and publishes OrderCancelRequested outbox event.
func (c *Client) CancelMMOrder(ctx context.Context, orderID string) error {
	_, err := c.client.CancelOrder(ctx, &orderv1.CancelOrderRequest{
		OrderId: orderID,
		UserId:  account.WalletUUIDStr,
	})
	if err != nil {
		return fmt.Errorf("cancel MM order in Order Service: %w", err)
	}
	return nil
}

// ListMMOrders fetches all OPEN and PARTIALLY_FILLED orders for MM-001 on a given market.
func (c *Client) ListMMOrders(ctx context.Context, marketID string) ([]order.OSOrder, error) {
	// Fetch OPEN orders
	// NOTE: Order Service DB stores user_id as UUID — must use WalletUUIDStr, not "MM-001"
	openResp, err := c.client.ListOrders(ctx, &orderv1.ListOrdersRequest{
		UserId:   account.WalletUUIDStr,
		MarketId: marketID,
		Status:   orderv1.OrderStatus_ORDER_STATUS_OPEN,
		Limit:    200, // well above the 24-order maximum per market
	})
	if err != nil {
		return nil, fmt.Errorf("ListOrders OPEN for %s: %w", marketID, err)
	}

	// Fetch PARTIALLY_FILLED orders
	partialResp, err := c.client.ListOrders(ctx, &orderv1.ListOrdersRequest{
		UserId:   account.WalletUUIDStr,
		MarketId: marketID,
		Status:   orderv1.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED,
		Limit:    200,
	})
	if err != nil {
		return nil, fmt.Errorf("ListOrders PARTIALLY_FILLED for %s: %w", marketID, err)
	}

	combined := append(openResp.Orders, partialResp.Orders...)
	result := make([]order.OSOrder, 0, len(combined))

	for _, o := range combined {
		if o.IdempotencyKey == "" {
			continue
		}
		levelID, gen, err := parseLevelFromClientOrderID(o.IdempotencyKey)
		if err != nil {
			c.logger.Warn("skipping order with unparseable idempotency_key",
				zap.String("idempotency_key", o.IdempotencyKey),
				zap.Error(err))
			continue
		}

		origQty, err := decimal.NewFromString(o.Quantity)
		if err != nil {
			c.logger.Warn("skipping order with invalid quantity",
				zap.String("order_id", o.Id),
				zap.Error(err))
			continue
		}
		remainQty, err := decimal.NewFromString(o.RemainingQuantity)
		if err != nil {
			c.logger.Warn("skipping order with invalid remaining_quantity",
				zap.String("order_id", o.Id),
				zap.Error(err))
			continue
		}
		price, err := decimal.NewFromString(o.Price)
		if err != nil {
			c.logger.Warn("skipping order with invalid price",
				zap.String("order_id", o.Id),
				zap.Error(err))
			continue
		}

		side := "BUY"
		if o.Side == orderv1.OrderSide_ORDER_SIDE_SELL {
			side = "SELL"
		}

		result = append(result, order.OSOrder{
			LevelID:       levelID,
			Generation:    gen,
			ClientOrderID: o.IdempotencyKey,
			OrderID:       o.Id,
			Side:          side,
			Price:         price,
			OriginalQty:   origQty,
			RemainingQty:  remainQty,
		})
	}

	return result, nil
}

// GetOrderByClientID looks up a specific MM order by its client_order_id (idempotency_key).
func (c *Client) GetOrderByClientID(ctx context.Context, clientOrderID string) (*OrderState, error) {
	var marketID string
	if levelID, _, err := parseLevelFromClientOrderID(clientOrderID); err == nil {
		marketID = parseMarketFromLevelID(levelID)
	}

	resp, err := c.client.ListOrders(ctx, &orderv1.ListOrdersRequest{
		UserId:   account.WalletUUIDStr, // Order Service DB: user_id is UUID type
		MarketId: marketID,
		Limit:    100,
	})
	if err != nil {
		return nil, fmt.Errorf("ListOrders for client_order_id lookup: %w", err)
	}

	for _, o := range resp.Orders {
		if o.IdempotencyKey == clientOrderID {
			origQty, _ := decimal.NewFromString(o.Quantity)
			remainQty, _ := decimal.NewFromString(o.RemainingQuantity)
			return &OrderState{
				OrderID:       o.Id,
				ClientOrderID: o.IdempotencyKey,
				Status:        protoStatusToString(o.Status),
				OriginalQty:   origQty,
				RemainingQty:  remainQty,
			}, nil
		}
	}

	return nil, ErrOrderNotFound
}

// IsAvailable performs a lightweight check to confirm the Order Service is reachable.
// Any error (DeadlineExceeded, Internal, Unavailable, etc.) returns false.
func (c *Client) IsAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := c.client.ListOrders(ctx, &orderv1.ListOrdersRequest{
		UserId: account.WalletUUIDStr,
		Limit:  1,
	})
	return err == nil
}

// RecoverHighestGenerations queries recent orders for the market maker across all statuses
// to recover the highest generation ever assigned to each level, guaranteeing monotonicity across restarts (INV-MM-07).
func (c *Client) RecoverHighestGenerations(ctx context.Context, marketID string) (map[string]int, error) {
	resp, err := c.client.ListOrders(ctx, &orderv1.ListOrdersRequest{
		UserId:   account.WalletUUIDStr,
		MarketId: marketID,
		Limit:    500,
	})
	if err != nil {
		return nil, fmt.Errorf("ListOrders for generation recovery on %s: %w", marketID, err)
	}

	maxGens := make(map[string]int)
	for _, o := range resp.Orders {
		if o.IdempotencyKey == "" {
			continue
		}
		levelID, gen, err := parseLevelFromClientOrderID(o.IdempotencyKey)
		if err == nil && gen > maxGens[levelID] {
			maxGens[levelID] = gen
		}
	}
	return maxGens, nil
}
