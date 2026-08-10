package wallet

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
)

// Client is an infrastructure adapter wrapping the gRPC Wallet Service client.
type Client struct {
	conn   *grpc.ClientConn
	grpc   walletv1.WalletServiceClient
	logger *zap.Logger
}

func NewClient(addr string, logger *zap.Logger) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create wallet client: %w", err)
	}

	return &Client{
		conn:   conn,
		grpc:   walletv1.NewWalletServiceClient(conn),
		logger: logger,
	}, nil
}

// Close closes the underlying gRPC transport connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// ReserveFunds locks funds in Wallet Service for an order placement.
func (c *Client) ReserveFunds(ctx context.Context, userID, orderID, asset, amount string) (string, bool, error) {
	resp, err := c.grpc.ReserveFunds(ctx, &walletv1.ReserveFundsRequest{
		UserId:  userID,
		OrderId: orderID,
		Asset:   asset,
		Amount:  amount,
	})
	if err != nil {
		return "", false, err
	}
	return resp.ReservationId, resp.AlreadyExisted, nil
}

// ReleaseFunds unlocks funds in Wallet Service when an order is cancelled.
func (c *Client) ReleaseFunds(ctx context.Context, orderID string) error {
	_, err := c.grpc.ReleaseFunds(ctx, &walletv1.ReleaseFundsRequest{
		OrderId: orderID,
	})
	return err
}
