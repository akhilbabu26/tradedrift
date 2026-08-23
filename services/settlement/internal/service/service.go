package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/settlement/internal/client"
	"tradedrift/services/settlement/internal/repository"
)

// TradeExecutedEvent is the deserialized payload of a `trades.executed` Kafka message.
// Fields map 1:1 to what the Matching Engine publisher writes.
type TradeExecutedEvent struct {
	TradeID      string `json:"trade_id"`
	MarketID     string `json:"market_id"`
	MakerOrderID string `json:"maker_order_id"`
	TakerOrderID string `json:"taker_order_id"`
	BuyOrderID   string `json:"buy_order_id"`
	SellOrderID  string `json:"sell_order_id"`
	BuyerUserID  string `json:"buyer_user_id"`
	SellerUserID string `json:"seller_user_id"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	ExecutedAt   string `json:"executed_at"` // RFC3339Nano
}

// WalletSettler is the interface the Service uses to call the Wallet Service.
// *client.WalletClient satisfies this interface — the indirection allows
// the service to be unit-tested with a lightweight mock.
type WalletSettler interface {
	SettleTrade(ctx context.Context, req client.SettleRequest) error
}

// Service implements the 3-phase settlement pipeline.
type Service struct {
	repo        repository.Repository
	wallet      WalletSettler
	log         *zap.Logger
	grpcTimeout time.Duration // per-RPC deadline for Wallet.SettleTrade
}

// NewService creates a new Service.
func NewService(repo repository.Repository, wallet WalletSettler, log *zap.Logger, grpcTimeout time.Duration) *Service {
	return &Service{repo: repo, wallet: wallet, log: log, grpcTimeout: grpcTimeout}
}

// Settle executes the 3-phase settlement pipeline for a single TradeExecuted event.
//
// Phase 1 (Short DB TX):
//
//	INSERT settled_trades (status=PENDING) ON CONFLICT DO NOTHING
//	DB connection released immediately after commit.
//
// Phase 2 (No DB conn held):
//
//	gRPC call to Wallet.SettleTrade — atomically moves funds.
//	Wallet-side idempotency (trade_id UNIQUE) absorbs duplicate calls safely.
//
// Phase 3 (Short DB TX):
//
//	UPDATE settled_trades SET status=SETTLED, settled_at=NOW()
//	DB connection released immediately after commit.
//
// Returns nil on success OR if trade was already SETTLED (no-op, safe to ACK).
// Returns an error for any failure — caller must NOT ACK the Kafka offset.
func (s *Service) Settle(ctx context.Context, event TradeExecutedEvent) error {
	// ── VALIDATE ALL UUIDs ─────────────────────────────────────────────────────
	// uuid.MustParse would panic on a malformed Kafka event — use explicit errors.
	tradeID, err := uuid.Parse(event.TradeID)
	if err != nil {
		return fmt.Errorf("invalid trade_id %q: %w", event.TradeID, err)
	}
	buyerID, err := uuid.Parse(event.BuyerUserID)
	if err != nil {
		return fmt.Errorf("invalid buyer_user_id %q: %w", event.BuyerUserID, err)
	}
	sellerID, err := uuid.Parse(event.SellerUserID)
	if err != nil {
		return fmt.Errorf("invalid seller_user_id %q: %w", event.SellerUserID, err)
	}
	buyOrderID, err := uuid.Parse(event.BuyOrderID)
	if err != nil {
		return fmt.Errorf("invalid buy_order_id %q: %w", event.BuyOrderID, err)
	}
	sellOrderID, err := uuid.Parse(event.SellOrderID)
	if err != nil {
		return fmt.Errorf("invalid sell_order_id %q: %w", event.SellOrderID, err)
	}

	// ── VALIDATE buyer ≠ seller ───────────────────────────────────────────────
	// A self-trade (buyer == seller) is a trading rule violation. Reject early
	// so no funds are moved and the poison event is skipped cleanly.
	if buyerID == sellerID {
		return errors.New("buyer and seller cannot be the same user")
	}

	// ── VALIDATE PRICE AND QUANTITY ────────────────────────────────────────────
	// Validate that both are well-formed positive decimals before touching any
	// database or making any gRPC call. A malformed value here would corrupt
	// wallet balances — fail fast and let Kafka redeliver the event.
	price, err := decimal.NewFromString(event.Price)
	if err != nil {
		return fmt.Errorf("invalid price %q: %w", event.Price, err)
	}
	if price.LessThanOrEqual(decimal.Zero) {
		return errors.New("price must be positive")
	}

	quantity, err := decimal.NewFromString(event.Quantity)
	if err != nil {
		return fmt.Errorf("invalid quantity %q: %w", event.Quantity, err)
	}
	if quantity.LessThanOrEqual(decimal.Zero) {
		return errors.New("quantity must be positive")
	}

	// ── IDEMPOTENCY CHECK ──────────────────────────────────────────────────────
	existing, err := s.repo.FindByTradeID(ctx, tradeID)
	if err != nil {
		return fmt.Errorf("idempotency check: %w", err)
	}
	if existing != nil && existing.Status == repository.StatusSettled {
		s.log.Info("trade already settled, ACK no-op",
			zap.String("trade_id", event.TradeID),
			zap.String("market", event.MarketID),
		)
		return nil // safe to ACK
	}

	// Parse market_id → base + quote assets: "BTC-USDT" → base="BTC", quote="USDT"
	base, quote, err := parseMarketID(event.MarketID)
	if err != nil {
		return fmt.Errorf("parse market_id: %w", err)
	}

	// ── PHASE 1 — SHORT DB TRANSACTION ────────────────────────────────────────
	// Only insert if not already in DB (existing may be PENDING from a prior crash).
	if existing == nil {
		executedAt, parseErr := time.Parse(time.RFC3339Nano, event.ExecutedAt)
		if parseErr != nil {
			// Fallback to RFC3339 if nanoseconds are absent
			executedAt, parseErr = time.Parse(time.RFC3339, event.ExecutedAt)
			if parseErr != nil {
				return fmt.Errorf("parse executed_at %q: %w", event.ExecutedAt, parseErr)
			}
		}

		trade := &repository.SettledTrade{
			TradeID:     tradeID,
			BuyerID:     buyerID,
			SellerID:    sellerID,
			BuyOrderID:  buyOrderID,
			SellOrderID: sellOrderID,
			MarketID:    event.MarketID,
			BaseAsset:   base,
			QuoteAsset:  quote,
			Price:       event.Price,
			Quantity:    event.Quantity,
			Status:      repository.StatusPending,
			ExecutedAt:  executedAt,
		}

		if err := s.repo.Insert(ctx, trade); err != nil {
			return fmt.Errorf("phase 1 insert: %w", err)
		}
		// DB connection is released here — Phase 2 runs with no open DB connection.
		s.log.Debug("phase 1 complete: registered PENDING",
			zap.String("trade_id", event.TradeID),
		)
	} else {
		// PENDING row exists from a prior attempt — skip Phase 1, retry Phase 2.
		s.log.Info("found PENDING row, retrying wallet settlement",
			zap.String("trade_id", event.TradeID),
			zap.String("market", event.MarketID),
		)
	}

	// ── PHASE 2 — gRPC (NO DB CONNECTION HELD) ────────────────────────────────
	// Wallet.SettleTrade is idempotent on trade_id — duplicate calls are absorbed.
	// A bounded context prevents a hung Wallet Service from stalling the consumer.
	rpcCtx, cancel := context.WithTimeout(ctx, s.grpcTimeout)
	defer cancel()

	if err := s.wallet.SettleTrade(rpcCtx, client.SettleRequest{
		TradeID:     event.TradeID,
		BuyerID:     event.BuyerUserID,
		SellerID:    event.SellerUserID,
		BuyOrderID:  event.BuyOrderID,
		SellOrderID: event.SellOrderID,
		BaseAsset:   base,
		QuoteAsset:  quote,
		Price:       event.Price,
		Quantity:    event.Quantity,
		MarketID:    event.MarketID,
	}); err != nil {
		// Do NOT return nil — caller must NOT ACK the Kafka offset.
		return fmt.Errorf("phase 2 wallet settle: %w", err)
	}

	// ── PHASE 3 — SHORT DB TRANSACTION ────────────────────────────────────────
	if err := s.repo.MarkSettled(ctx, tradeID); err != nil {
		return fmt.Errorf("phase 3 mark settled: %w", err)
	}

	s.log.Info("✅ SETTLED",
		zap.String("trade_id", event.TradeID),
		zap.String("market", event.MarketID),
		zap.String("base_asset", base),
		zap.String("quote_asset", quote),
		zap.String("price", event.Price),
		zap.String("quantity", event.Quantity),
		zap.String("buyer", event.BuyerUserID),
		zap.String("seller", event.SellerUserID),
	)
	return nil
}

// RecoverStalePending is called by the background recovery goroutine every 60s.
// It scans for PENDING rows whose created_at is older than 60 seconds and retries
// Phase 2 + Phase 3 for each.
//
// NOTE on concurrency: FOR UPDATE SKIP LOCKED in FindStalePending prevents the
// recovery goroutine from selecting rows already row-locked by a concurrent Phase 3
// UPDATE in the Kafka consumer. However, since the gRPC call happens OUTSIDE a
// transaction (no DB connection held during the gRPC call), the lock is released
// before Wallet.SettleTrade returns. This means duplicate gRPC calls are theoretically
// possible if both the Kafka consumer and recovery goroutine select the same PENDING
// row. This is safe because Wallet.SettleTrade is idempotent on trade_id — duplicate
// calls are absorbed without double-settling. A future RECOVERING status state could
// eliminate the redundant network call if needed.
func (s *Service) RecoverStalePending(ctx context.Context) {
	const (
		staleThreshold = 60 * time.Second
		batchLimit     = 50
	)

	trades, err := s.repo.FindStalePending(ctx, staleThreshold, batchLimit)
	if err != nil {
		s.log.Error("recovery: failed to query stale PENDING trades", zap.Error(err))
		return
	}

	if len(trades) == 0 {
		return
	}

	s.log.Info("recovery: found stale PENDING trades", zap.Int("count", len(trades)))

	for _, t := range trades {
		// Each trade gets its own bounded timeout — a stuck Wallet gRPC call for
		// one trade must not stall recovery of all subsequent trades in the batch.
		rpcCtx, cancel := context.WithTimeout(ctx, s.grpcTimeout)
		err := s.wallet.SettleTrade(rpcCtx, client.SettleRequest{
			TradeID:     t.TradeID.String(),
			BuyerID:     t.BuyerID.String(),
			SellerID:    t.SellerID.String(),
			BuyOrderID:  t.BuyOrderID.String(),
			SellOrderID: t.SellOrderID.String(),
			BaseAsset:   t.BaseAsset,
			QuoteAsset:  t.QuoteAsset,
			Price:       t.Price,
			Quantity:    t.Quantity,
			MarketID:    t.MarketID,
		})
		cancel() // release resources immediately after each RPC, not at function return

		if err != nil {
			s.log.Error("recovery: wallet settle failed, will retry next cycle",
				zap.String("trade_id", t.TradeID.String()),
				zap.Error(err),
			)
			continue
		}

		if err := s.repo.MarkSettled(ctx, t.TradeID); err != nil {
			s.log.Error("recovery: mark settled failed",
				zap.String("trade_id", t.TradeID.String()),
				zap.Error(err),
			)
			continue
		}

		s.log.Info("recovery: ✅ recovered PENDING trade",
			zap.String("trade_id", t.TradeID.String()),
			zap.String("market", t.MarketID),
		)
	}
}

// parseMarketID splits "BTC-USDT" into ("BTC", "USDT").
// Returns an error if the format is invalid.
func parseMarketID(marketID string) (base, quote string, err error) {
	parts := strings.SplitN(marketID, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid market_id format %q: expected BASE-QUOTE", marketID)
	}
	return parts[0], parts[1], nil
}
