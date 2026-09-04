package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/repository"
)

// TradeSettlementRequest holds all data needed to settle a single trade.
// All fields are required unless noted. They flow from Settlement Service → Wallet Service
// via the SettleTrade gRPC call, and from there into the outbox payloads published to
// trades.settled.v1 (for Trade Service) and portfolio.user.trades.v1 (for Portfolio Service).
type TradeSettlementRequest struct {
	TradeID       string // UUIDv7 — Matching Engine generated, idempotency key
	BuyerUserID   string // buyer's user UUID
	SellerUserID  string // seller's user UUID
	BuyOrderID    string // matched buy order UUID
	SellerOrderID string // matched sell order UUID (used to look up the reservation)
	MarketID      string // e.g. "BTC-USDT"
	BaseAsset     string // e.g. "BTC" — what changes hands
	QuoteAsset    string // e.g. "USDT" — what was paid
	BaseAmount    string // How much BTC the buyer receives (= quantity)
	QuoteAmount   string // How much USDT the seller receives (= price × quantity)
	Price         string // authoritative match price (decimal string)
	Quantity      string // authoritative match quantity (decimal string)
	Sequence      uint64 // ME per-market monotonic counter (> 0)
	ExecutedAt    string // RFC3339Nano — Matching Engine clock
}

// SettleTrade atomically settles a matched trade within a single PostgreSQL transaction:
//   - Registers settlement identity in settled_trades (ON CONFLICT DO NOTHING for primary idempotency)
//   - Locks both seller and buyer reservations in sorted order using SELECT ... FOR UPDATE (preventing deadlocks)
//   - Debits seller's reserved BaseAsset and consumes seller's reservation
//   - Credits buyer's available BaseAsset
//   - Debits buyer's reserved QuoteAsset and consumes buyer's reservation
//   - Credits seller's available QuoteAsset
//   - Writes 4 immutable ledger entries (seller Base DEBIT, buyer Base CREDIT, buyer Quote DEBIT, seller Quote CREDIT)
//   - Writes 3 outbox events (1 TradeSettled for Trade Service, 2 PortfolioUserTrade for buyer/seller legs)
//
// All mutations commit atomically or roll back completely on failure.
func (s *Service) SettleTrade(ctx context.Context, req TradeSettlementRequest) error {
	// Step 0: Validate domain invariants
	if req.TradeID == "" || req.BuyerUserID == "" || req.SellerUserID == "" || req.BuyOrderID == "" || req.SellerOrderID == "" {
		return fmt.Errorf("%w: missing required trade identifiers", repository.ErrInvalidSettlement)
	}
	if req.BuyerUserID == req.SellerUserID {
		return fmt.Errorf("%w: self-trade not permitted", repository.ErrInvalidSettlement)
	}
	if req.BuyOrderID == req.SellerOrderID {
		return fmt.Errorf("%w: buy and sell order IDs cannot be identical", repository.ErrInvalidSettlement)
	}
	if req.BaseAsset == "" || req.QuoteAsset == "" {
		return fmt.Errorf("%w: base_asset and quote_asset are required", repository.ErrInvalidSettlement)
	}
	if req.BaseAmount == "" || req.BaseAmount == "0" || strings.HasPrefix(req.BaseAmount, "-") {
		return fmt.Errorf("%w: invalid base amount %q", repository.ErrInvalidSettlement, req.BaseAmount)
	}
	if req.QuoteAmount == "" || req.QuoteAmount == "0" || strings.HasPrefix(req.QuoteAmount, "-") {
		return fmt.Errorf("%w: invalid quote amount %q", repository.ErrInvalidSettlement, req.QuoteAmount)
	}

	// Step 1: Begin single atomic PostgreSQL transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin settlement transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Bind transaction to repository instances
	walletRepo := s.walletRepo.WithTx(tx)
	reservRepo := s.reservRepo.WithTx(tx)
	txnRepo := s.txnRepo.WithTx(tx)
	outboxRepo := s.outboxRepo.WithTx(tx)
	settledTradeRepo := s.settledTradeRepo.WithTx(tx)

	// Step 2: Idempotency check via dedicated settled_trades table
	inserted, err := settledTradeRepo.RegisterSettlement(ctx, req.TradeID, req.MarketID, req.Sequence)
	if err != nil {
		return fmt.Errorf("failed to register settled trade: %w", err)
	}
	if !inserted {
		s.log.Debug("trade already settled, skipping", zap.String("tradeID", req.TradeID))
		return nil
	}

	// Step 3: Lock both reservations in deterministic sorted order (SELECT ... FOR UPDATE)
	// Eliminates deadlock hazards when concurrent crossed orders settle simultaneously.
	firstOrderID, secondOrderID := req.BuyOrderID, req.SellerOrderID
	if firstOrderID > secondOrderID {
		firstOrderID, secondOrderID = secondOrderID, firstOrderID
	}

	res1, err := reservRepo.GetByOrderIDForUpdate(ctx, firstOrderID)
	if err != nil {
		return fmt.Errorf("failed to fetch reservation for order %s: %w", firstOrderID, err)
	}
	if res1 == nil {
		return fmt.Errorf("%w: reservation not found for order %s", repository.ErrReservationNotFound, firstOrderID)
	}

	res2, err := reservRepo.GetByOrderIDForUpdate(ctx, secondOrderID)
	if err != nil {
		return fmt.Errorf("failed to fetch reservation for order %s: %w", secondOrderID, err)
	}
	if res2 == nil {
		return fmt.Errorf("%w: reservation not found for order %s", repository.ErrReservationNotFound, secondOrderID)
	}

	var buyerRes, sellerRes *repository.Reservation
	if res1.OrderID == req.BuyOrderID {
		buyerRes, sellerRes = res1, res2
	} else {
		buyerRes, sellerRes = res2, res1
	}

	// Step 4: Validate reservations
	// Seller must have reserved BaseAsset (BTC)
	if sellerRes.Status == repository.ReservationReleased {
		return fmt.Errorf("%w: seller reservation already released for order %s", repository.ErrInsufficientReservation, req.SellerOrderID)
	}
	if sellerRes.Asset != req.BaseAsset {
		return fmt.Errorf("%w: seller reservation asset %s does not match trade base asset %s", repository.ErrInvalidSettlement, sellerRes.Asset, req.BaseAsset)
	}
	if sellerRes.UserID != req.SellerUserID {
		return fmt.Errorf("%w: seller reservation user_id %s does not match seller %s", repository.ErrInvalidSettlement, sellerRes.UserID, req.SellerUserID)
	}

	// Buyer must have reserved QuoteAsset (USDT)
	if buyerRes.Status == repository.ReservationReleased {
		return fmt.Errorf("%w: buyer reservation already released for order %s", repository.ErrInsufficientReservation, req.BuyOrderID)
	}
	if buyerRes.Asset != req.QuoteAsset {
		return fmt.Errorf("%w: buyer reservation asset %s does not match trade quote asset %s", repository.ErrInvalidSettlement, buyerRes.Asset, req.QuoteAsset)
	}
	if buyerRes.UserID != req.BuyerUserID {
		return fmt.Errorf("%w: buyer reservation user_id %s does not match buyer %s", repository.ErrInvalidSettlement, buyerRes.UserID, req.BuyerUserID)
	}

	// Step 5: Leg 1 — Base Asset Transfer (Seller -> Buyer)
	// 5a. Debit seller's reserved BaseAsset
	sellerBaseWallet, err := walletRepo.GetByUserAndAsset(ctx, req.SellerUserID, req.BaseAsset)
	if err != nil {
		return fmt.Errorf("failed to fetch seller base wallet: %w", err)
	}
	if sellerBaseWallet == nil {
		return fmt.Errorf("seller base wallet not found for asset %s", req.BaseAsset)
	}
	if err := walletRepo.DebitReserved(ctx, sellerBaseWallet.ID, req.BaseAmount); err != nil {
		return fmt.Errorf("failed to debit seller reserved base balance: %w", err)
	}

	// 5b. Atomically consume seller reservation remaining amount
	if err := reservRepo.ConsumeRemaining(ctx, sellerRes.ID, req.BaseAmount); err != nil {
		return fmt.Errorf("failed to consume seller reservation: %w", err)
	}

	// 5c. Credit buyer's available BaseAsset
	buyerBaseWallet, err := walletRepo.GetByUserAndAsset(ctx, req.BuyerUserID, req.BaseAsset)
	if err != nil {
		return fmt.Errorf("failed to fetch buyer base wallet: %w", err)
	}
	if buyerBaseWallet == nil {
		return fmt.Errorf("buyer base wallet not found for asset %s", req.BaseAsset)
	}
	if err := walletRepo.CreditAvailable(ctx, buyerBaseWallet.ID, req.BaseAmount); err != nil {
		return fmt.Errorf("failed to credit buyer available base balance: %w", err)
	}

	// Step 6: Leg 2 — Quote Asset Transfer (Buyer -> Seller)
	// 6a. Debit buyer's reserved QuoteAsset
	buyerQuoteWallet, err := walletRepo.GetByUserAndAsset(ctx, req.BuyerUserID, req.QuoteAsset)
	if err != nil {
		return fmt.Errorf("failed to fetch buyer quote wallet: %w", err)
	}
	if buyerQuoteWallet == nil {
		return fmt.Errorf("buyer quote wallet not found for asset %s", req.QuoteAsset)
	}
	if err := walletRepo.DebitReserved(ctx, buyerQuoteWallet.ID, req.QuoteAmount); err != nil {
		return fmt.Errorf("failed to debit buyer reserved quote balance: %w", err)
	}

	// 6b. Atomically consume buyer reservation remaining amount
	if err := reservRepo.ConsumeRemaining(ctx, buyerRes.ID, req.QuoteAmount); err != nil {
		return fmt.Errorf("failed to consume buyer reservation: %w", err)
	}

	// 6c. Credit seller's available QuoteAsset
	sellerQuoteWallet, err := walletRepo.GetByUserAndAsset(ctx, req.SellerUserID, req.QuoteAsset)
	if err != nil {
		return fmt.Errorf("failed to fetch seller quote wallet: %w", err)
	}
	if sellerQuoteWallet == nil {
		return fmt.Errorf("seller quote wallet not found for asset %s", req.QuoteAsset)
	}
	if err := walletRepo.CreditAvailable(ctx, sellerQuoteWallet.ID, req.QuoteAmount); err != nil {
		return fmt.Errorf("failed to credit seller available quote balance: %w", err)
	}

	now := time.Now().UTC()

	// Step 7: Write 4 ledger entries (Seller Base DEBIT, Buyer Base CREDIT, Buyer Quote DEBIT, Seller Quote CREDIT)
	sellerBaseTxnID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate seller base transaction ID: %w", err)
	}
	buyerBaseTxnID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate buyer base transaction ID: %w", err)
	}
	buyerQuoteTxnID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate buyer quote transaction ID: %w", err)
	}
	sellerQuoteTxnID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate seller quote transaction ID: %w", err)
	}

	txns := []*repository.WalletTransaction{
		{
			ID:              sellerBaseTxnID,
			WalletID:        sellerBaseWallet.ID,
			ReferenceID:     req.TradeID,
			ReferenceType:   repository.RefSettlement,
			TransactionType: repository.TxnTypeDebit,
			Asset:           req.BaseAsset,
			Amount:          req.BaseAmount,
			CreatedAt:       now,
		},
		{
			ID:              buyerBaseTxnID,
			WalletID:        buyerBaseWallet.ID,
			ReferenceID:     req.TradeID,
			ReferenceType:   repository.RefSettlement,
			TransactionType: repository.TxnTypeCredit,
			Asset:           req.BaseAsset,
			Amount:          req.BaseAmount,
			CreatedAt:       now,
		},
		{
			ID:              buyerQuoteTxnID,
			WalletID:        buyerQuoteWallet.ID,
			ReferenceID:     req.TradeID,
			ReferenceType:   repository.RefSettlement,
			TransactionType: repository.TxnTypeDebit,
			Asset:           req.QuoteAsset,
			Amount:          req.QuoteAmount,
			CreatedAt:       now,
		},
		{
			ID:              sellerQuoteTxnID,
			WalletID:        sellerQuoteWallet.ID,
			ReferenceID:     req.TradeID,
			ReferenceType:   repository.RefSettlement,
			TransactionType: repository.TxnTypeCredit,
			Asset:           req.QuoteAsset,
			Amount:          req.QuoteAmount,
			CreatedAt:       now,
		},
	}
	if err := txnRepo.CreateBatch(ctx, txns); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			s.log.Warn("duplicate settlement transaction detected via DB unique constraint, treating as idempotent",
				zap.String("tradeID", req.TradeID),
			)
			return nil
		}
		return fmt.Errorf("failed to write settlement ledger entries: %w", err)
	}

	// Step 8: Write 3 Outbox Events inside the same transaction

	// Event 1: TradeSettled (consumed by Trade Service, partitioned by BuyerUserID)
	eventID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate outbox event ID: %w", err)
	}
	type tradeSettledPayload struct {
		TradeID     string `json:"trade_id"`
		BuyerID     string `json:"buyer_id"`
		SellerID    string `json:"seller_id"`
		BuyOrderID  string `json:"buy_order_id"`
		SellOrderID string `json:"sell_order_id"`
		MarketID    string `json:"market_id"`
		BaseAsset   string `json:"base_asset"`
		QuoteAsset  string `json:"quote_asset"`
		Price       string `json:"price"`
		Quantity    string `json:"quantity"`
		Sequence    uint64 `json:"sequence"`
		ExecutedAt  string `json:"executed_at"` // RFC3339Nano — ME clock
		SettledAt   string `json:"settled_at"`  // RFC3339Nano — Wallet clock
	}
	tradeSettledBytes, err := json.Marshal(tradeSettledPayload{
		TradeID:     req.TradeID,
		BuyerID:     req.BuyerUserID,
		SellerID:    req.SellerUserID,
		BuyOrderID:  req.BuyOrderID,
		SellOrderID: req.SellerOrderID,
		MarketID:    req.MarketID,
		BaseAsset:   req.BaseAsset,
		QuoteAsset:  req.QuoteAsset,
		Price:       req.Price,
		Quantity:    req.Quantity,
		Sequence:    req.Sequence,
		ExecutedAt:  req.ExecutedAt,
		SettledAt:   now.Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("failed to marshal TradeSettled payload: %w", err)
	}
	if err := outboxRepo.Insert(ctx, &repository.OutboxEvent{
		ID:           eventID,
		AggregateID:  req.TradeID,
		EventType:    "TradeSettled",
		Payload:      tradeSettledBytes,
		PartitionKey: req.BuyerUserID,
		CreatedAt:    now,
	}); err != nil {
		return fmt.Errorf("failed to insert TradeSettled outbox event: %w", err)
	}

	// Events 2 & 3: Dual user-scoped accounting events for Portfolio Service.
	// Preserves strict Kafka log order per user and eliminates dual-participant partition hazards.
	type userTradePayload struct {
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

	// Buyer Leg (BUY)
	buyerEventID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate buyer portfolio outbox ID: %w", err)
	}
	buyerPayload, err := json.Marshal(userTradePayload{
		TradeID:    req.TradeID,
		UserID:     req.BuyerUserID,
		OrderID:    req.BuyOrderID,
		Role:       "BUY",
		MarketID:   req.MarketID,
		BaseAsset:  req.BaseAsset,
		QuoteAsset: req.QuoteAsset,
		Price:      req.Price,
		Quantity:   req.Quantity,
		Sequence:   req.Sequence,
		ExecutedAt: req.ExecutedAt,
		SettledAt:  now.Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("failed to marshal buyer portfolio payload: %w", err)
	}
	if err := outboxRepo.Insert(ctx, &repository.OutboxEvent{
		ID:           buyerEventID,
		AggregateID:  req.TradeID,
		EventType:    "PortfolioUserTrade",
		Payload:      buyerPayload,
		PartitionKey: req.BuyerUserID,
		CreatedAt:    now,
	}); err != nil {
		return fmt.Errorf("failed to insert buyer portfolio outbox event: %w", err)
	}

	// Seller Leg (SELL)
	sellerEventID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate seller portfolio outbox ID: %w", err)
	}
	sellerPayload, err := json.Marshal(userTradePayload{
		TradeID:    req.TradeID,
		UserID:     req.SellerUserID,
		OrderID:    req.SellerOrderID,
		Role:       "SELL",
		MarketID:   req.MarketID,
		BaseAsset:  req.BaseAsset,
		QuoteAsset: req.QuoteAsset,
		Price:      req.Price,
		Quantity:   req.Quantity,
		Sequence:   req.Sequence,
		ExecutedAt: req.ExecutedAt,
		SettledAt:  now.Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("failed to marshal seller portfolio payload: %w", err)
	}
	if err := outboxRepo.Insert(ctx, &repository.OutboxEvent{
		ID:           sellerEventID,
		AggregateID:  req.TradeID,
		EventType:    "PortfolioUserTrade",
		Payload:      sellerPayload,
		PartitionKey: req.SellerUserID,
		CreatedAt:    now,
	}); err != nil {
		return fmt.Errorf("failed to insert seller portfolio outbox event: %w", err)
	}

	// Step 9: Commit all changes atomically
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit settlement transaction: %w", err)
	}

	s.log.Info("trade settled atomically (two-sided)",
		zap.String("tradeID", req.TradeID),
		zap.String("buyerUserID", req.BuyerUserID),
		zap.String("sellerUserID", req.SellerUserID),
		zap.String("baseAsset", req.BaseAsset),
		zap.String("baseAmount", req.BaseAmount),
		zap.String("quoteAsset", req.QuoteAsset),
		zap.String("quoteAmount", req.QuoteAmount),
	)

	return nil
}
