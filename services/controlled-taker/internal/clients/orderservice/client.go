// Package orderservice provides a gRPC client for Order Service to submit aggressive crossing limit orders.
package orderservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	orderv1 "tradedrift/platform/api/gen/order/v1"
	"tradedrift/services/controlled-taker/internal/account"
)

// OrderResult represents the result returned by Order Service.
type OrderResult struct {
	OrderID       string
	ClientOrderID string
	Status        string
	RemainingQty  string
	FilledQty     string
}

// Client is a wrapper around orderv1.OrderServiceClient.
type Client struct {
	conn   *grpc.ClientConn
	client orderv1.OrderServiceClient
	logger *zap.Logger
}

// NewClient dials the Order Service at addr.
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

// NewClientWithServiceClient creates a Client using an existing orderv1.OrderServiceClient (for testing).
func NewClientWithServiceClient(client orderv1.OrderServiceClient, logger *zap.Logger) *Client {
	return &Client{
		client: client,
		logger: logger,
	}
}

// Close closes the gRPC connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Ping checks if the gRPC connection is ready and Order Service is capable of serving RPCs.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.client == nil {
		return errors.New("order service client is not initialized")
	}
	if c.conn != nil {
		state := c.conn.GetState()
		if state == connectivity.TransientFailure || state == connectivity.Shutdown {
			return fmt.Errorf("order service connection state is %s", state)
		}
	}
	pingCtx, pingCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer pingCancel()

	_, err := c.client.ListOrders(pingCtx, &orderv1.ListOrdersRequest{
		UserId: account.WalletUUIDStr,
		Limit:  1,
	})
	if err != nil {
		return fmt.Errorf("order service RPC check failed: %w", err)
	}
	return nil
}

// CreateCrossingOrder submits an aggressive limit order with slippage price cap/floor.
func (c *Client) CreateCrossingOrder(ctx context.Context, marketID, side, priceCap, quantity, idempotencyKey string) (*OrderResult, error) {
	protoSide := orderv1.OrderSide_ORDER_SIDE_BUY
	if side == "SELL" {
		protoSide = orderv1.OrderSide_ORDER_SIDE_SELL
	}

	req := &orderv1.CreateOrderRequest{
		UserId:         account.WalletUUIDStr,
		MarketId:       marketID,
		Side:           protoSide,
		OrderType:      orderv1.OrderType_ORDER_TYPE_LIMIT,
		Price:          priceCap,
		Quantity:       quantity,
		IdempotencyKey: idempotencyKey,
	}

	start := time.Now()
	resp, err := c.client.CreateOrder(ctx, req)
	duration := time.Since(start)

	if err != nil {
		c.logger.Warn("CreateOrder failed",
			zap.String("market", marketID),
			zap.String("side", side),
			zap.String("price", priceCap),
			zap.String("qty", quantity),
			zap.Duration("duration", duration),
			zap.Error(err),
		)
		return nil, fmt.Errorf("OrderService.CreateOrder: %w", err)
	}

	o := resp.GetOrder()
	if o == nil || o.GetId() == "" {
		return nil, errors.New("OrderService.CreateOrder: response contains nil or empty order")
	}

	c.logger.Info("CreateOrder succeeded",
		zap.String("order_id", o.GetId()),
		zap.String("market", marketID),
		zap.String("side", side),
		zap.String("status", o.GetStatus().String()),
		zap.Duration("duration", duration),
	)

	return &OrderResult{
		OrderID:       o.GetId(),
		ClientOrderID: idempotencyKey,
		Status:        o.GetStatus().String(),
		RemainingQty:  o.GetRemainingQuantity(),
		FilledQty:     o.GetFilledQuantity(),
	}, nil
}

// GetOrder fetches the latest status of an order from Order Service.
func (c *Client) GetOrder(ctx context.Context, orderID string) (*OrderResult, error) {
	req := &orderv1.GetOrderRequest{
		OrderId: orderID,
		UserId:  account.WalletUUIDStr,
	}

	resp, err := c.client.GetOrder(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("OrderService.GetOrder: %w", err)
	}

	o := resp.GetOrder()
	if o == nil || o.GetId() == "" {
		return nil, fmt.Errorf("OrderService.GetOrder: response for %s contains nil or empty order", orderID)
	}

	return &OrderResult{
		OrderID:       o.GetId(),
		ClientOrderID: o.GetIdempotencyKey(),
		Status:        o.GetStatus().String(),
		RemainingQty:  o.GetRemainingQuantity(),
		FilledQty:     o.GetFilledQuantity(),
	}, nil
}

// CancelOrder requests cancellation of an active order residual to prevent CT-001 from resting as a maker.
func (c *Client) CancelOrder(ctx context.Context, orderID string) (*OrderResult, error) {
	req := &orderv1.CancelOrderRequest{
		OrderId: orderID,
		UserId:  account.WalletUUIDStr,
	}

	resp, err := c.client.CancelOrder(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("OrderService.CancelOrder: %w", err)
	}

	o := resp.GetOrder()
	if o == nil || o.GetId() == "" {
		return nil, fmt.Errorf("OrderService.CancelOrder: response for %s contains nil or empty order", orderID)
	}

	return &OrderResult{
		OrderID:       o.GetId(),
		ClientOrderID: o.GetIdempotencyKey(),
		Status:        o.GetStatus().String(),
		RemainingQty:  o.GetRemainingQuantity(),
		FilledQty:     o.GetFilledQuantity(),
	}, nil
}

// FindOrderByIdempotencyKey searches recent orders for the given idempotency key.
// Order Service returns orders sorted by created_at DESC, id DESC with keyset cursor pagination.
// This method queries up to maxPages (3 pages of 50 orders = 150 recent orders) to exhaustively
// check if CT-001's order was committed during an ambiguous submission failure.
// Returns (OrderResult, nil) if found, (nil, nil) if exhaustively absent, or (nil, err) on RPC failure.
func (c *Client) FindOrderByIdempotencyKey(ctx context.Context, marketID, idempotencyKey string) (*OrderResult, error) {
	if c.client == nil {
		return nil, errors.New("OrderService client is nil")
	}

	const pageSize = 50
	const maxPages = 3

	cursor := ""
	for page := 0; page < maxPages; page++ {
		req := &orderv1.ListOrdersRequest{
			UserId:   account.WalletUUIDStr,
			MarketId: marketID,
			Limit:    pageSize,
			Cursor:   cursor,
		}

		resp, err := c.client.ListOrders(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("OrderService.ListOrders (page %d): %w", page+1, err)
		}

		orders := resp.GetOrders()
		for _, o := range orders {
			if o.GetIdempotencyKey() == idempotencyKey {
				return &OrderResult{
					OrderID:       o.GetId(),
					ClientOrderID: o.GetIdempotencyKey(),
					Status:        o.GetStatus().String(),
					RemainingQty:  o.GetRemainingQuantity(),
					FilledQty:     o.GetFilledQuantity(),
				}, nil
			}
		}

		cursor = resp.GetNextCursor()
		if cursor == "" || len(orders) < pageSize {
			// No further pages available; order was exhaustively absent
			break
		}
	}

	return nil, nil
}
