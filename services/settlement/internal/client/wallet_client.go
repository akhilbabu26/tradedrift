package client

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
)

// SettleRequest holds all fields needed for one Wallet.SettleTrade gRPC call.
// Field names map directly to the TradeExecuted Kafka event payload.
type SettleRequest struct {
	TradeID      string
	BuyerID      string
	SellerID     string
	BuyOrderID   string
	SellOrderID  string
	BaseAsset    string
	QuoteAsset   string
	Price        string
	Quantity     string
	MarketID     string
}

// WalletClient wraps the generated gRPC WalletServiceClient.
type WalletClient struct {
	conn   *grpc.ClientConn
	client walletv1.WalletServiceClient
}

// NewWalletClient dials the Wallet Service and returns a ready-to-use client.
// The caller must call Close() when done.
func NewWalletClient(addr string) (*WalletClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial wallet service at %s: %w", addr, err)
	}
	return &WalletClient{
		conn:   conn,
		client: walletv1.NewWalletServiceClient(conn),
	}, nil
}

// SettleTrade calls Wallet.SettleTrade and returns an error if it fails.
// This call is idempotent on the Wallet side — duplicate calls for the same
// trade_id are silently absorbed via the wallet_transactions UNIQUE constraint.
func (c *WalletClient) SettleTrade(ctx context.Context, req SettleRequest) error {
	_, err := c.client.SettleTrade(ctx, &walletv1.SettleTradeRequest{
		TradeId:     req.TradeID,
		BuyerId:     req.BuyerID,
		SellerId:    req.SellerID,
		BuyOrderId:  req.BuyOrderID,
		SellOrderId: req.SellOrderID,
		BaseAsset:   req.BaseAsset,
		QuoteAsset:  req.QuoteAsset,
		Price:       req.Price,
		Quantity:    req.Quantity,
		MarketId:    req.MarketID,
	})
	if err != nil {
		return fmt.Errorf("wallet SettleTrade gRPC: %w", err)
	}
	return nil
}

// Close releases the underlying gRPC connection.
func (c *WalletClient) Close() error {
	return c.conn.Close()
}
