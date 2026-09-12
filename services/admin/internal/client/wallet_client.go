package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
)

// WalletClient wraps the Wallet gRPC client with correlation metadata injection.
type WalletClient struct {
	client walletv1.WalletServiceClient
	conn   *grpc.ClientConn
}

// NewWalletClient creates a gRPC connection to the Wallet service.
func NewWalletClient(grpcAddr string) (*WalletClient, error) {
	conn, err := grpc.Dial(grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("wallet_client: dial %s: %w", grpcAddr, err)
	}
	return &WalletClient{client: walletv1.NewWalletServiceClient(conn), conn: conn}, nil
}

// Close releases the underlying gRPC connection.
func (c *WalletClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Ping performs a real Health RPC check to verify the Wallet service is actually responding.
func (c *WalletClient) Ping(ctx context.Context) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("wallet_client: client is nil")
	}
	callCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	_, err := c.client.Health(callCtx, &walletv1.HealthRequest{})
	if err != nil {
		return fmt.Errorf("wallet_client: health rpc failed: %w", err)
	}
	return nil
}

// FreezeWallet calls the Wallet service to freeze or unfreeze a user's wallet asset.
// The operation is naturally idempotent on the Wallet side.
// Times out after 5 seconds and injects distributed tracing headers.
func (c *WalletClient) FreezeWallet(ctx context.Context, userID, asset, reason, requestID, operationID string, freeze bool) (bool, error) {
	if c == nil || c.client == nil {
		return false, fmt.Errorf("wallet_client: client is nil")
	}

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Propagate distributed tracing headers into gRPC metadata.
	md := metadata.Pairs(
		"x-request-id", requestID,
		"x-operation-id", operationID,
	)
	callCtx = metadata.NewOutgoingContext(callCtx, md)

	resp, err := c.client.FreezeWallet(callCtx, &walletv1.FreezeWalletRequest{
		UserId: userID,
		Asset:  asset,
		Freeze: freeze,
		Reason: reason,
	})
	if err != nil {
		return false, fmt.Errorf("wallet_client: FreezeWallet: %w", err)
	}
	if !resp.Success {
		return false, fmt.Errorf("wallet_client: FreezeWallet: wallet service returned success=false")
	}
	return resp.IsFrozen, nil
}
