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

var (
	ErrInvalidUserID = errors.New("invalid user_id: must be a valid UUID")
	ErrEmptyTitle    = errors.New("notification title cannot be empty")
	ErrEmptyMessage  = errors.New("notification message cannot be empty")
)

// Inbound domain event payloads for Kafka ingestion
type TradeSettledEvent struct {
	TradeID      string `json:"trade_id"`
	MarketID     string `json:"market_id"`
	BaseAsset    string `json:"base_asset"`
	QuoteAsset   string `json:"quote_asset"`
	BuyerUserID  string `json:"buyer_user_id"`
	SellerUserID string `json:"seller_user_id"`
	BuyOrderID   string `json:"buy_order_id"`
	SellOrderID  string `json:"sell_order_id"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	Sequence     uint64 `json:"sequence"`
	ExecutedAt   string `json:"executed_at"`
}

type OrderCancelledEvent struct {
	EventID   string `json:"event_id"`
	OrderID   string `json:"order_id"`
	UserID    string `json:"user_id"`
	MarketID  string `json:"market_id"`
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

type PortfolioUpdatedEvent struct {
	EventID       string `json:"event_id"`
	UserID        string `json:"user_id"`
	TotalValue    string `json:"total_value"`
	RealizedPnL   string `json:"realized_pnl"`
	UnrealizedPnL string `json:"unrealized_pnl"`
	CashBalance   string `json:"cash_balance"`
	UpdatedAt     string `json:"updated_at"`
}

type Service struct {
	repo repository.NotificationRepository
	log  *zap.Logger
}

func NewService(repo repository.NotificationRepository, log *zap.Logger) *Service {
	return &Service{
		repo: repo,
		log:  log,
	}
}

// HandleTradeSettled processes a TradeSettled Kafka event, performing strict counterparty privacy isolation.
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

// HandleOrderCancelled processes an OrderCancelled Kafka event.
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

// HandlePortfolioUpdated stages real-time portfolio updates into the outbox for Redis streaming.
// Note: Portfolio updates are ephemeral state syncs and do NOT create persistent rows in notifications.
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

// CreateNotification allows internal microservices (Auth, Admin, Wallet) to dispatch user alerts.
func (s *Service) CreateNotification(ctx context.Context, input model.CreateNotificationInput) (*model.Notification, error) {
	if input.UserID == "" {
		return nil, ErrInvalidUserID
	}
	if input.Title == "" {
		return nil, ErrEmptyTitle
	}
	if input.Message == "" {
		return nil, ErrEmptyMessage
	}

	now := time.Now().UTC()
	notifID, _ := platformuuid.New()
	outboxID, _ := platformuuid.New()

	notifType := input.Type
	if notifType == "" {
		notifType = model.TypeInfo
	}

	notif := &model.Notification{
		ID:            notifID,
		UserID:        input.UserID,
		Title:         input.Title,
		Message:       input.Message,
		Type:          notifType,
		ReferenceID:   input.ReferenceID,
		ReferenceType: input.ReferenceType,
		IsRead:        false,
		CreatedAt:     now,
	}

	env := model.RedisEnvelope{
		EventID:        notifID,
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
		TargetChannel: "user:notifications:" + input.UserID,
		CreatedAt:     now,
	}

	if err := s.repo.CreateWithDedupTx(ctx, notif, notifID, outbox); err != nil {
		return nil, fmt.Errorf("create notification: %w", err)
	}

	return notif, nil
}

// GetNotifications retrieves a paginated inbox for the user.
func (s *Service) GetNotifications(ctx context.Context, filter model.PaginationFilter) ([]*model.Notification, error) {
	if filter.UserID == "" {
		return nil, ErrInvalidUserID
	}
	return s.repo.GetByUserID(ctx, filter)
}

// MarkAsRead marks a single notification as read, enforcing user ownership.
func (s *Service) MarkAsRead(ctx context.Context, userID, notificationID string) (*model.Notification, error) {
	if userID == "" {
		return nil, ErrInvalidUserID
	}
	if notificationID == "" {
		return nil, errors.New("invalid notification_id")
	}
	return s.repo.MarkAsRead(ctx, userID, notificationID)
}

// MarkAllAsRead marks all unread notifications as read for a user.
func (s *Service) MarkAllAsRead(ctx context.Context, userID string) (int32, error) {
	if userID == "" {
		return 0, ErrInvalidUserID
	}
	return s.repo.MarkAllAsRead(ctx, userID)
}

// GetUnreadCount returns the count of unread notifications for a user.
func (s *Service) GetUnreadCount(ctx context.Context, userID string) (int32, error) {
	if userID == "" {
		return 0, ErrInvalidUserID
	}
	return s.repo.GetUnreadCount(ctx, userID)
}
