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
// Deduplication key: ev.EventID (the source domain event identifier).
// One source event creates two independent notification rows — one per counterparty.
// The processed_events table records ev.EventID so that a Kafka redelivery of the
// same event is detected and the second run is a no-op.
//
// Counterparty privacy:
//   - The buyer notification contains only their own order details.
//   - The seller notification contains only their own order details.
//   - Neither message exposes the other party's user ID or order ID.
//
// Field contract: event_id, trade_id, buyer_user_id, seller_user_id,
// buy_order_id, and sell_order_id must all be canonical UUIDs.
func (s *Service) HandleTradeSettled(ctx context.Context, ev *TradeSettledEvent) error {
	if ev.EventID == "" || ev.TradeID == "" || ev.BuyerUserID == "" || ev.SellerUserID == "" {
		return errors.New("invalid trade settled event: missing required fields (event_id, trade_id, buyer_user_id, seller_user_id)")
	}
	// Validate all fields that map to PostgreSQL UUID columns.
	for _, f := range []struct{ name, val string }{
		{"event_id", ev.EventID},
		{"trade_id", ev.TradeID},
		{"buyer_user_id", ev.BuyerUserID},
		{"seller_user_id", ev.SellerUserID},
		{"buy_order_id", ev.BuyOrderID},
		{"sell_order_id", ev.SellOrderID},
	} {
		// BuyOrderID / SellOrderID may be empty for non-standard trade types; only validate when present.
		if f.name == "buy_order_id" || f.name == "sell_order_id" {
			if f.val == "" {
				continue
			}
		}
		if err := validateUUID(f.name, f.val); err != nil {
			return err
		}
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
		EventID:        ev.EventID, // source event ID — same for buyer and seller
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
		EventID:        ev.EventID, // same source event ID
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

	// 3. Atomically persist both notifications and outbox records,
	//    deduplicated by ev.EventID (not ev.TradeID) via processed_events.
	err := s.repo.CreateTradeSettledTx(ctx, buyerNotif, sellerNotif, ev.EventID, buyerOutbox, sellerOutbox)
	if err != nil {
		if errors.Is(err, repository.ErrAlreadyProcessed) {
			s.log.Debug("TradeSettled event already processed; skipping duplicate",
				zap.String("event_id", ev.EventID),
				zap.String("trade_id", ev.TradeID),
			)
			return nil
		}
		return fmt.Errorf("create trade settled notifications: %w", err)
	}

	s.log.Info("Processed TradeSettled notifications",
		zap.String("event_id", ev.EventID),
		zap.String("trade_id", ev.TradeID),
		zap.String("buyer_notification_id", buyerNotifID),
		zap.String("seller_notification_id", sellerNotifID),
	)
	return nil
}

// HandleOrderCancelled processes an OrderCancelled Kafka event and creates a persistent
// notification for the order owner.
//
// Field contract: event_id, order_id, and user_id must be canonical UUIDs.
// event_id is mandatory — it is the deduplication identity, not a fallback.
func (s *Service) HandleOrderCancelled(ctx context.Context, ev *OrderCancelledEvent) error {
	if ev.EventID == "" || ev.OrderID == "" || ev.UserID == "" {
		return errors.New("invalid order cancelled event: missing required fields (event_id, order_id, user_id)")
	}
	for _, f := range []struct{ name, val string }{
		{"event_id", ev.EventID},
		{"order_id", ev.OrderID},
		{"user_id", ev.UserID},
	} {
		if err := validateUUID(f.name, f.val); err != nil {
			return err
		}
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

	env := model.RedisEnvelope{
		EventID:        ev.EventID, // source domain event ID — no fallback
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

	err := s.repo.CreateWithDedupTx(ctx, notif, ev.EventID, outbox)
	if err != nil {
		if errors.Is(err, repository.ErrAlreadyProcessed) {
			s.log.Debug("OrderCancelled event already processed; skipping duplicate",
				zap.String("event_id", ev.EventID),
				zap.String("order_id", ev.OrderID),
			)
			return nil
		}
		return fmt.Errorf("create order cancelled notification: %w", err)
	}

	s.log.Info("Processed OrderCancelled notification",
		zap.String("event_id", ev.EventID),
		zap.String("order_id", ev.OrderID),
		zap.String("user_id", ev.UserID),
	)
	return nil
}

// HandlePortfolioUpdated stages a real-time portfolio snapshot into the outbox for Redis streaming.
//
// Delivery semantics — at-least-once, duplicate-tolerant:
//
//	Portfolio updates represent the current state of a user's portfolio at a point in
//	time. Unlike trade notifications they do NOT create persistent rows in the
//	notifications table; only an outbox row is written. If Kafka redelivers the same
//	event (ev.EventID), the Gateway receives a duplicate Redis publish and deduplicates
//	it using the (event_id, notification_id) pair already in the WebSocket stream.
//	Two publishes of the same portfolio snapshot are harmless — the client simply
//	renders the same state twice.
//
//	Deduplication via processed_events is intentionally omitted here because:
//	  1. The state is idempotent — duplicate snapshots carry the same data.
//	  2. Portfolio events are high-frequency; writing to processed_events for each
//	     would significantly increase DB write amplification with no correctness benefit.
//
// Field contract: event_id and user_id are mandatory and must be canonical UUIDs.
// There is no synthesised fallback for a missing event_id — a message without one
// must be routed to the DLQ at the consumer layer.
func (s *Service) HandlePortfolioUpdated(ctx context.Context, ev *PortfolioUpdatedEvent) error {
	if ev.EventID == "" || ev.UserID == "" {
		return errors.New("invalid portfolio updated event: missing required fields (event_id, user_id)")
	}
	for _, f := range []struct{ name, val string }{
		{"event_id", ev.EventID},
		{"user_id", ev.UserID},
	} {
		if err := validateUUID(f.name, f.val); err != nil {
			return err
		}
	}

	now := time.Now().UTC()
	outboxID, _ := platformuuid.New()

	env := model.RedisEnvelope{
		EventID:        ev.EventID,
		NotificationID: "", // portfolio updates have no persistent notification
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
