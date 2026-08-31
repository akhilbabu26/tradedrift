// Package walletservice provides a read-only gRPC client for the Wallet Service.
// The LE uses this to fetch authoritative MM-001 balances on startup and periodically.
package walletservice

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/services/liquidity-engine/internal/account"
)

// Client is a read-only client for the Wallet Service.
type Client struct {
	conn   *grpc.ClientConn
	client walletv1.WalletServiceClient
	logger *zap.Logger
}

// NewClient dials the Wallet Service and returns a ready client.
func NewClient(addr string, logger *zap.Logger) (*Client, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial Wallet Service at %s: %w", addr, err)
	}
	return &Client{
		conn:   conn,
		client: walletv1.NewWalletServiceClient(conn),
		logger: logger,
	}, nil
}

// Close closes the gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// GetMMBalances fetches all asset balances for the MM-001 system account.
func (c *Client) GetMMBalances(ctx context.Context) (map[string]decimal.Decimal, error) {
	resp, err := c.client.GetBalances(ctx, &walletv1.GetBalancesRequest{
		UserId: account.WalletUUIDStr,
	})
	if err != nil {
		return nil, fmt.Errorf("GetBalances for MM-001: %w", err)
	}

	balances := make(map[string]decimal.Decimal, len(resp.Balances))
	for _, b := range resp.Balances {
		if b == nil || b.Asset == "" {
			continue
		}
		bal, err := decimal.NewFromString(b.AvailableBalance)
		if err != nil {
			c.logger.Warn("invalid balance decimal string from wallet service",
				zap.String("asset", b.Asset),
				zap.String("available_balance", b.AvailableBalance),
				zap.Error(err))
			continue
		}
		balances[b.Asset] = bal
	}

	return balances, nil
}

// IsAvailable performs a lightweight check to confirm the Wallet Service is reachable.
// Any error — including DeadlineExceeded, Internal, Unknown — means unhealthy.
func (c *Client) IsAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := c.client.GetBalances(ctx, &walletv1.GetBalancesRequest{
		UserId: account.WalletUUIDStr,
	})
	return err == nil
}
