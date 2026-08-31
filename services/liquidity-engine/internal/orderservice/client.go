// Package orderservice provides a read-only gRPC client for the Order Service.
// The LE uses this for:
//   1. Startup discovery: ListMMOrders to populate the tracker
//   2. PENDING timeout check: GetOrderByClientID to confirm order state
//   3. CANCELLING timeout check: GetOrderByClientID to confirm cancel
//   4. STALE resolution: triggered via full resync (ListMMOrders again)
//
// The LE NEVER writes to the Order Service (no CreateOrder, no CancelOrder via gRPC).
// Orders are created/cancelled via Kafka commands only (orders.commands topic).
package orderservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	orderv1 "tradedrift/platform/api/gen/order/v1"
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

// ListMMOrders fetches all OPEN and PARTIALLY_FILLED orders for MM-001 on a given market.
func (c *Client) ListMMOrders(ctx context.Context, marketID string) ([]order.OSOrder, error) {
	// Fetch OPEN orders
	openResp, err := c.client.ListOrders(ctx, &orderv1.ListOrdersRequest{
		UserId:   "MM-001",
		MarketId: marketID,
		Status:   orderv1.OrderStatus_ORDER_STATUS_OPEN,
		Limit:    200, // well above the 24-order maximum per market
	})
	if err != nil {
		return nil, fmt.Errorf("ListOrders OPEN for %s: %w", marketID, err)
	}

	// Fetch PARTIALLY_FILLED orders
	partialResp, err := c.client.ListOrders(ctx, &orderv1.ListOrdersRequest{
		UserId:   "MM-001",
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

		_ = gen // generation is stored in tracker.generations, not in OSOrder

		side := "BUY"
		if o.Side == orderv1.OrderSide_ORDER_SIDE_SELL {
			side = "SELL"
		}

		result = append(result, order.OSOrder{
			LevelID:       levelID,
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
	resp, err := c.client.ListOrders(ctx, &orderv1.ListOrdersRequest{
		UserId: "MM-001",
		Limit:  200,
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
func (c *Client) IsAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := c.client.ListOrders(ctx, &orderv1.ListOrdersRequest{
		UserId: "MM-001",
		Limit:  1,
	})
	if err != nil {
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.Unavailable {
			return false
		}
	}
	return true
}

// parseLevelFromClientOrderID extracts the LevelID and generation from a client_order_id.
// Format: "MM-BTC-USDT-ASK-01-G003" -> ("MM-BTC-USDT-ASK-01", 3, nil)
func parseLevelFromClientOrderID(clientOrderID string) (levelID string, gen int, err error) {
	lastG := -1
	for i := len(clientOrderID) - 1; i >= 2; i-- {
		if clientOrderID[i-1] == '-' && clientOrderID[i] == 'G' {
			lastG = i - 1
			break
		}
	}
	if lastG < 0 {
		return "", 0, fmt.Errorf("no '-G' generation suffix found in %q", clientOrderID)
	}

	levelID = clientOrderID[:lastG]
	genStr := clientOrderID[lastG+2:]

	_, err = fmt.Sscanf(genStr, "%d", &gen)
	if err != nil {
		return "", 0, fmt.Errorf("invalid generation %q in %q: %w", genStr, clientOrderID, err)
	}
	return levelID, gen, nil
}

// protoStatusToString converts an Order Service proto status to string.
func protoStatusToString(s orderv1.OrderStatus) string {
	switch s {
	case orderv1.OrderStatus_ORDER_STATUS_OPEN:
		return "OPEN"
	case orderv1.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED:
		return "PARTIALLY_FILLED"
	case orderv1.OrderStatus_ORDER_STATUS_FILLED:
		return "FILLED"
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLING:
		return "CANCELLING"
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
		return "CANCELLED"
	default:
		return "UNKNOWN"
	}
}
