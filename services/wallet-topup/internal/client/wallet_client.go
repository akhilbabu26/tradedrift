package client

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
)

type WalletClient struct {
	client walletv1.WalletServiceClient
	conn   *grpc.ClientConn
}

func NewWalletClient(grpcAddr string) (*WalletClient, error) {
	conn, err := grpc.Dial(grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create wallet gRPC client for %s: %w", grpcAddr, err)
	}

	return &WalletClient{
		client: walletv1.NewWalletServiceClient(conn),
		conn:   conn,
	}, nil
}


func NewWalletClientWithConn(conn *grpc.ClientConn) *WalletClient {
	return &WalletClient{
		client: walletv1.NewWalletServiceClient(conn),
		conn:   conn,
	}
}

func NewWalletClientWithServiceClient(c walletv1.WalletServiceClient) *WalletClient {
	return &WalletClient{
		client: c,
	}
}

func (c *WalletClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type DepositResult struct {
	Success       bool
	TransactionID string
	NewBalance    string
}

func (c *WalletClient) DepositFunds(ctx context.Context, userID, asset, amount, refID, refType string) (*DepositResult, error) {
	req := &walletv1.DepositFundsRequest{
		UserId:        userID,
		Asset:         asset,
		Amount:        amount,
		ReferenceId:   refID,
		ReferenceType: refType,
	}

	resp, err := c.client.DepositFunds(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}

	return &DepositResult{
		Success:       resp.Success,
		TransactionID: resp.TransactionId,
		NewBalance:    resp.NewBalance,
	}, nil
}
