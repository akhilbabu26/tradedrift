package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/repository"
)

// HandleTradeSettled processes a TradeSettled Kafka event.
//
// Counterparty privacy design:
//   - The buyer receives a notification that contains only their own order details.
//   - The seller receives a notification that contains only their own order details.
//   - Neither side receives the other's user ID or order ID.
//
// Both notifications and both outbox rows are committed in a single PostgreSQL
// transaction, deduplicated by TradeID via the processed_events table.
func (s *Service) HandleTradeSettled(ctx context.Context, ev *TradeSettledEvent) error {
	if ev.TradeID == "" || ev.BuyerUserID == "" || ev.SellerUserID == "" {
		return errors.New("invalid trade settled event: missing required IDs")
	}

	now := time.Now().UTC()
	buyerNotifID, _ := platformuuid.New()
	sellerNotifID, _ := platformuuid.New()
	buyerOutboxID, _ := platformuuid.New()
	sellerOutboxID, _ := platformuuid.New()

	// 1. Buyer notification — sanitized to hide Seller ID
	buyerNotif := &model.Notification{
		ID:            buyerNotifID,
		UserID:        ev.BuyerUserID,
		Title:         "Trade Executed",
		Message:       fmt.Sprintf("Your BUY order of %s %s on %s filled at %s %s", ev.Quantity, ev.BaseAsset, ev.MarketID, ev.Price, ev.QuoteAsset),
		Type:          model.TypeTradeFill,
		ReferenceID:   ev.TradeID,
		ReferenceType: model.RefTypeTrade,
		IsRead:        false,
		CreatedAt:     now,
	}

	buyerEnv := model.RedisEnvelope{
		EventID:        ev.TradeID,
		NotificationID: buyerNotifID,
		Type:           "notification.created",
		Channel:        "user:notifications",
		Timestamp:      now,
		Data:           buyerNotif,
	}
	buyerPayload, _ := json.Marshal(buyerEnv)
	buyerOutbox := &model.OutboxEvent{
		ID:            buyerOutboxID,
		EventType:     "NotificationCreated",
		Payload:       buyerPayload,
		TargetChannel: "user:notifications:" + ev.BuyerUserID,
		CreatedAt:     now,
	}

	// 2. Seller notification — sanitized to hide Buyer ID
	sellerNotif := &model.Notification{
		ID:            sellerNotifID,
		UserID:        ev.SellerUserID,
		Title:         "Trade Executed",
		Message:       fmt.Sprintf("Your SELL order of %s %s on %s filled at %s %s", ev.Quantity, ev.BaseAsset, ev.MarketID, ev.Price, ev.QuoteAsset),
		Type:          model.TypeTradeFill,
		ReferenceID:   ev.TradeID,
		ReferenceType: model.RefTypeTrade,
		IsRead:        false,
		CreatedAt:     now,
	}

	sellerEnv := model.RedisEnvelope{
		EventID:        ev.TradeID,
		NotificationID: sellerNotifID,
		Type:           "notification.created",
		Channel:        "user:notifications",
		Timestamp:      now,
		Data:           sellerNotif,
	}
	sellerPayload, _ := json.Marshal(sellerEnv)
	sellerOutbox := &model.OutboxEvent{
		ID:            sellerOutboxID,
		EventType:     "NotificationCreated",
		Payload:       sellerPayload,
		TargetChannel: "user:notifications:" + ev.SellerUserID,
		CreatedAt:     now,
	}

	// 3. Atomically persist both notifications and outbox records with deduplication on TradeID
	err := s.repo.CreateTradeSettledTx(ctx, buyerNotif, sellerNotif, ev.TradeID, buyerOutbox, sellerOutbox)
	if err != nil {
		if errors.Is(err, repository.ErrAlreadyProcessed) {
			s.log.Debug("TradeSettled event already processed; skipping duplicate", zap.String("trade_id", ev.TradeID))
			return nil
		}
		return fmt.Errorf("create trade settled notifications: %w", err)
	}

	s.log.Info("Processed TradeSettled notifications",
		zap.String("trade_id", ev.TradeID),
		zap.String("buyer_id", ev.BuyerUserID),
		zap.String("seller_id", ev.SellerUserID),
	)
	return nil
}

// HandleOrderCancelled processes an OrderCancelled Kafka event and creates a persistent
// notification for the order owner.
func (s *Service) HandleOrderCancelled(ctx context.Context, ev *OrderCancelledEvent) error {
	if ev.OrderID == "" || ev.UserID == "" {
		return errors.New("invalid order cancelled event: missing order_id or user_id")
	}

	now := time.Now().UTC()
	notifID, _ := platformuuid.New()
	outboxID, _ := platformuuid.New()

	reason := ev.Reason
	if reason == "" {
		reason = "cancelled by user"
	}

	notif := &model.Notification{
		ID:            notifID,
		UserID:        ev.UserID,
		Title:         "Order Cancelled",
		Message:       fmt.Sprintf("Your order %s on %s was cancelled: %s", ev.OrderID, ev.MarketID, reason),
		Type:          model.TypeSystem,
		ReferenceID:   ev.OrderID,
		ReferenceType: model.RefTypeOrder,
		IsRead:        false,
		CreatedAt:     now,
	}

	sourceID := ev.EventID
	if sourceID == "" {
		sourceID = ev.OrderID
	}

	env := model.RedisEnvelope{
		EventID:        sourceID,
		NotificationID: notifID,
		Type:           "notification.created",
		Channel:        "user:notifications",
		Timestamp:      now,
		Data:           notif,
	}
	payload, _ := json.Marshal(env)
	outbox := &model.OutboxEvent{
		ID:            outboxID,
		EventType:     "NotificationCreated",
		Payload:       payload,
		TargetChannel: "user:notifications:" + ev.UserID,
		CreatedAt:     now,
	}

	err := s.repo.CreateWithDedupTx(ctx, notif, sourceID, outbox)
	if err != nil {
		if errors.Is(err, repository.ErrAlreadyProcessed) {
			s.log.Debug("OrderCancelled event already processed; skipping duplicate", zap.String("order_id", ev.OrderID))
			return nil
		}
		return fmt.Errorf("create order cancelled notification: %w", err)
	}

	s.log.Info("Processed OrderCancelled notification", zap.String("order_id", ev.OrderID), zap.String("user_id", ev.UserID))
	return nil
}

// HandlePortfolioUpdated stages a real-time portfolio snapshot into the outbox for Redis streaming.
//
// Portfolio updates are ephemeral state syncs — they do NOT create persistent rows in the
// notifications table. Only an outbox row is written so the publisher can broadcast the
// snapshot to the user's portfolio Redis channel.
func (s *Service) HandlePortfolioUpdated(ctx context.Context, ev *PortfolioUpdatedEvent) error {
	if ev.UserID == "" {
		return errors.New("invalid portfolio updated event: missing user_id")
	}

	now := time.Now().UTC()
	outboxID, _ := platformuuid.New()

	sourceID := ev.EventID
	if sourceID == "" {
		sourceID = fmt.Sprintf("portfolio:%s:%d", ev.UserID, now.UnixNano())
	}

	env := model.RedisEnvelope{
		EventID:        sourceID,
		NotificationID: "",
		Type:           "portfolio.updated",
		Channel:        "user:portfolio",
		Timestamp:      now,
		Data:           ev,
	}
	payload, _ := json.Marshal(env)

	outbox := &model.OutboxEvent{
		ID:            outboxID,
		EventType:     "PortfolioUpdated",
		Payload:       payload,
		TargetChannel: "user:portfolio:" + ev.UserID,
		CreatedAt:     now,
	}

	return s.repo.StageOutboxEvent(ctx, outbox)
}
