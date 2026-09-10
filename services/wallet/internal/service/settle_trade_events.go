package service

import (
	"encoding/json"
	"fmt"
	"time"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/repository"
)

// tradeSettledPayload is the JSON schema for the TradeSettled outbox event.
// Consumed by the Notification Service via the trades.settled.v1 Kafka topic.
// Field names must match the notification service's TradeSettledEvent struct exactly.
type tradeSettledPayload struct {
	EventID      string `json:"event_id"`       // outbox row UUID — deduplication key in notification service
	TradeID      string `json:"trade_id"`
	BuyerUserID  string `json:"buyer_user_id"`
	SellerUserID string `json:"seller_user_id"`
	BuyerID      string `json:"buyer_id,omitempty"`
	SellerID     string `json:"seller_id,omitempty"`
	BuyOrderID   string `json:"buy_order_id"`
	SellOrderID  string `json:"sell_order_id"`
	MarketID     string `json:"market_id"`
	BaseAsset    string `json:"base_asset"`
	QuoteAsset   string `json:"quote_asset"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	Sequence     uint64 `json:"sequence"`
	ExecutedAt   string `json:"executed_at"` // RFC3339Nano — ME clock
	SettledAt    string `json:"settled_at"`  // RFC3339Nano — Wallet clock
}

// portfolioUserTradePayload is the JSON schema for PortfolioUserTrade outbox events.
// One event is emitted per trade participant via portfolio.user.trades.v1.
// Partition key = user_id — preserves strict per-user FIFO ordering in Kafka.
type portfolioUserTradePayload struct {
	TradeID    string `json:"trade_id"`
	UserID     string `json:"user_id"`
	OrderID    string `json:"order_id"`
	Role       string `json:"role"` // "BUY" or "SELL"
	MarketID   string `json:"market_id"`
	BaseAsset  string `json:"base_asset"`
	QuoteAsset string `json:"quote_asset"`
	Price      string `json:"price"`
	Quantity   string `json:"quantity"`
	Sequence   uint64 `json:"sequence"`
	ExecutedAt string `json:"executed_at"`
	SettledAt  string `json:"settled_at"`
}

// buildTradeSettledEvent constructs the TradeSettled outbox event for the Notification Service.
// Partitioned by BuyerUserID so that all events for a buyer land in the same Kafka partition.
func buildTradeSettledEvent(req TradeSettlementRequest, settledAt time.Time) (*repository.OutboxEvent, error) {
	id, err := platformuuid.New()
	if err != nil {
		return nil, fmt.Errorf("failed to generate TradeSettled event ID: %w", err)
	}
	payload, err := json.Marshal(tradeSettledPayload{
		EventID:      id,                // outbox row UUID used by notification service for deduplication
		TradeID:      req.TradeID,
		BuyerUserID:  req.BuyerUserID,
		SellerUserID: req.SellerUserID,
		BuyerID:      req.BuyerUserID,
		SellerID:     req.SellerUserID,
		BuyOrderID:   req.BuyOrderID,
		SellOrderID:  req.SellerOrderID,
		MarketID:     req.MarketID,
		BaseAsset:    req.BaseAsset,
		QuoteAsset:   req.QuoteAsset,
		Price:        req.Price,
		Quantity:     req.Quantity,
		Sequence:     req.Sequence,
		ExecutedAt:   req.ExecutedAt,
		SettledAt:    settledAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal TradeSettled payload: %w", err)
	}
	return &repository.OutboxEvent{
		ID:           id,
		AggregateID:  req.TradeID,
		EventType:    "TradeSettled",
		Payload:      payload,
		PartitionKey: req.BuyerUserID, // route to buyer's Kafka partition
		CreatedAt:    settledAt,
	}, nil
}


// buildPortfolioEvent constructs a user-scoped PortfolioUserTrade outbox event.
// role must be "BUY" (for the buyer leg) or "SELL" (for the seller leg).
// Partitioned by userID so all events for a given user are strictly ordered.
func buildPortfolioEvent(req TradeSettlementRequest, userID, orderID, role string, settledAt time.Time) (*repository.OutboxEvent, error) {
	id, err := platformuuid.New()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PortfolioUserTrade event ID (%s): %w", role, err)
	}
	payload, err := json.Marshal(portfolioUserTradePayload{
		TradeID:    req.TradeID,
		UserID:     userID,
		OrderID:    orderID,
		Role:       role,
		MarketID:   req.MarketID,
		BaseAsset:  req.BaseAsset,
		QuoteAsset: req.QuoteAsset,
		Price:      req.Price,
		Quantity:   req.Quantity,
		Sequence:   req.Sequence,
		ExecutedAt: req.ExecutedAt,
		SettledAt:  settledAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal PortfolioUserTrade payload (%s): %w", role, err)
	}
	return &repository.OutboxEvent{
		ID:           id,
		AggregateID:  req.TradeID,
		EventType:    "PortfolioUserTrade",
		Payload:      payload,
		PartitionKey: userID, // route to this user's Kafka partition
		CreatedAt:    settledAt,
	}, nil
}
