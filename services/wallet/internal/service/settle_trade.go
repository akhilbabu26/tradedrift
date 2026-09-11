package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
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
//
//   - Validates all request fields and financial invariants (see settle_trade_validate.go)
//   - Registers settlement identity in settled_trades (ON CONFLICT DO NOTHING — primary idempotency)
//   - Locks both reservations in sorted order (SELECT ... FOR UPDATE — prevents reservation deadlocks)
//   - Locks all four wallet rows in sorted id order (LockByIDs — prevents wallet deadlocks)
//   - Leg 1: Debits seller reserved BaseAsset, consumes seller reservation, credits buyer available BaseAsset
//   - Leg 2: Debits buyer reserved QuoteAsset, consumes buyer reservation, credits seller available QuoteAsset
//   - Writes 4 immutable ledger entries (seller Base DEBIT, buyer Base CREDIT, buyer Quote DEBIT, seller Quote CREDIT)
//   - Writes 3 outbox events (TradeSettled for Trade Service, PortfolioUserTrade×2 for buyer/seller)
//     (see settle_trade_events.go for payload schemas and builders)
//
// All mutations commit atomically or roll back completely on failure.
func (s *Service) SettleTrade(ctx context.Context, req TradeSettlementRequest) error {
	// ── Step 0: Domain invariant validation (pure in-memory, no I/O) ──────────────────────────────

	if req.TradeID == "" || req.BuyerUserID == "" || req.SellerUserID == "" ||
		req.BuyOrderID == "" || req.SellerOrderID == "" {
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
	if req.MarketID == "" {
		return fmt.Errorf("%w: market_id is required", repository.ErrInvalidSettlement)
	}
	if req.Sequence == 0 {
		return fmt.Errorf("%w: sequence must be > 0", repository.ErrInvalidSettlement)
	}
	// Financial field validation: decimal parsing, positivity, scale, cross-field invariants.
	if err := validateSettlementAmounts(ctx, s.assetRepo, req); err != nil {
		return err
	}

	// ── Step 1: Begin atomic PostgreSQL transaction ───────────────────────────────────────────────

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin settlement transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	walletRepo := s.walletRepo.WithTx(tx)
	reservRepo := s.reservRepo.WithTx(tx)
	txnRepo := s.txnRepo.WithTx(tx)
	outboxRepo := s.outboxRepo.WithTx(tx)
	settledTradeRepo := s.settledTradeRepo.WithTx(tx)

	// ── Step 2: Idempotency — register settlement (ON CONFLICT DO NOTHING) ───────────────────────
	// Returns (false, ErrSettlementConflict) if the same TradeID arrives with different
	// market_id or sequence — indicating upstream replay corruption, not a safe duplicate.

	inserted, err := settledTradeRepo.RegisterSettlement(ctx, req.TradeID, req.MarketID, req.Sequence)
	if err != nil {
		return fmt.Errorf("failed to register settled trade: %w", err)
	}
	if !inserted {
		s.log.Debug("trade already settled, skipping", zap.String("tradeID", req.TradeID))
		return nil
	}

	// ── Step 3: Lock reservations in deterministic sorted order (SELECT ... FOR UPDATE) ──────────
	// Acquiring in min(buyOrderID, sellOrderID) order prevents crossed-order deadlocks.
	// MM account (00000000-0000-0000-0000-000000000001) manages inventory locally and has no reservation.

	const mmAccountUUID = "00000000-0000-0000-0000-000000000001"
	isBuyerMM := (req.BuyerUserID == mmAccountUUID)
	isSellerMM := (req.SellerUserID == mmAccountUUID)

	var buyerRes, sellerRes *repository.Reservation

	if !isBuyerMM && !isSellerMM {
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

		if res1.OrderID == req.BuyOrderID {
			buyerRes, sellerRes = res1, res2
		} else {
			buyerRes, sellerRes = res2, res1
		}
	} else if !isBuyerMM {
		res, err := reservRepo.GetByOrderIDForUpdate(ctx, req.BuyOrderID)
		if err != nil {
			return fmt.Errorf("failed to fetch reservation for order %s: %w", req.BuyOrderID, err)
		}
		if res == nil {
			return fmt.Errorf("%w: reservation not found for order %s", repository.ErrReservationNotFound, req.BuyOrderID)
		}
		buyerRes = res
	} else if !isSellerMM {
		res, err := reservRepo.GetByOrderIDForUpdate(ctx, req.SellerOrderID)
		if err != nil {
			return fmt.Errorf("failed to fetch reservation for order %s: %w", req.SellerOrderID, err)
		}
		if res == nil {
			return fmt.Errorf("%w: reservation not found for order %s", repository.ErrReservationNotFound, req.SellerOrderID)
		}
		sellerRes = res
	}

	// ── Step 4: Validate reservation state and ownership ─────────────────────────────────────────

	if !isSellerMM {
		if sellerRes.Status == repository.ReservationReleased {
			return fmt.Errorf("%w: seller reservation already released for order %s", repository.ErrInsufficientReservation, req.SellerOrderID)
		}
		if sellerRes.Asset != req.BaseAsset {
			return fmt.Errorf("%w: seller reservation asset %s does not match trade base asset %s", repository.ErrInvalidSettlement, sellerRes.Asset, req.BaseAsset)
		}
		if sellerRes.UserID != req.SellerUserID {
			return fmt.Errorf("%w: seller reservation user_id %s does not match seller %s", repository.ErrInvalidSettlement, sellerRes.UserID, req.SellerUserID)
		}
	}

	if !isBuyerMM {
		if buyerRes.Status == repository.ReservationReleased {
			return fmt.Errorf("%w: buyer reservation already released for order %s", repository.ErrInsufficientReservation, req.BuyOrderID)
		}
		if buyerRes.Asset != req.QuoteAsset {
			return fmt.Errorf("%w: buyer reservation asset %s does not match trade quote asset %s", repository.ErrInvalidSettlement, buyerRes.Asset, req.QuoteAsset)
		}
		if buyerRes.UserID != req.BuyerUserID {
			return fmt.Errorf("%w: buyer reservation user_id %s does not match buyer %s", repository.ErrInvalidSettlement, buyerRes.UserID, req.BuyerUserID)
		}
	}

	// ── Step 4b: Assert buyer quote amount does not exceed remaining reservation ─────────────────
	// The wallet ledger must never silently alter executed trade economics or shortchange the seller.
	// If the matched trade quote exceeds the buyer's remaining reservation, settlement fails.
	if !isBuyerMM && buyerRes != nil {
		qAmt, qErr := decimal.NewFromString(req.QuoteAmount)
		rAmt, rErr := decimal.NewFromString(buyerRes.RemainingAmount)
		if qErr == nil && rErr == nil && qAmt.GreaterThan(rAmt) {
			return fmt.Errorf("%w: quote_amount %s exceeds buyer remaining reservation %s",
				repository.ErrInsufficientReservation, req.QuoteAmount, buyerRes.RemainingAmount)
		}
	}


	// ── Step 5: Fetch all four affected wallet IDs (read-only, no lock yet) ──────────────────────

	sellerBaseWallet, err := walletRepo.GetByUserAndAsset(ctx, req.SellerUserID, req.BaseAsset)
	if err != nil {
		return fmt.Errorf("failed to fetch seller base wallet: %w", err)
	}
	if sellerBaseWallet == nil {
		return fmt.Errorf("seller base wallet not found for asset %s", req.BaseAsset)
	}

	buyerBaseWallet, err := walletRepo.GetByUserAndAsset(ctx, req.BuyerUserID, req.BaseAsset)
	if err != nil {
		return fmt.Errorf("failed to fetch buyer base wallet: %w", err)
	}
	if buyerBaseWallet == nil {
		return fmt.Errorf("buyer base wallet not found for asset %s", req.BaseAsset)
	}

	buyerQuoteWallet, err := walletRepo.GetByUserAndAsset(ctx, req.BuyerUserID, req.QuoteAsset)
	if err != nil {
		return fmt.Errorf("failed to fetch buyer quote wallet: %w", err)
	}
	if buyerQuoteWallet == nil {
		return fmt.Errorf("buyer quote wallet not found for asset %s", req.QuoteAsset)
	}

	sellerQuoteWallet, err := walletRepo.GetByUserAndAsset(ctx, req.SellerUserID, req.QuoteAsset)
	if err != nil {
		return fmt.Errorf("failed to fetch seller quote wallet: %w", err)
	}
	if sellerQuoteWallet == nil {
		return fmt.Errorf("seller quote wallet not found for asset %s", req.QuoteAsset)
	}

	// ── Step 5b: Per-asset decimal precision check ─────────────────────────────────────────────
	// supported_assets.decimals is the authoritative precision for each asset.
	// QuoteAmount is computed as price × quantity at the handler layer. Floating-point
	// multiplication can produce more decimal places than the quote asset allows
	// (e.g. 96411.43 × 0.1 = 9641.143 → 3dp, but USDT allows only 2).
	// Floor-truncate QuoteAmount to the quote asset's precision BEFORE validation so that
	// all downstream writes (ledger, outbox, wallet balance mutations) use the rounded value.
	roundedQuoteAmount, err := roundToAssetPrecision(ctx, s.assetRepo, req.QuoteAsset, req.QuoteAmount)
	if err != nil {
		return fmt.Errorf("round quote_amount to asset precision: %w", err)
	}
	req.QuoteAmount = roundedQuoteAmount

	if err := validateAssetPrecision(ctx, s.assetRepo, req.BaseAsset, "base_amount", req.BaseAmount); err != nil {
		return err
	}
	if err := validateAssetPrecision(ctx, s.assetRepo, req.QuoteAsset, "quote_amount", req.QuoteAmount); err != nil {
		return err
	}

	// ── Step 6: Lock all four wallet rows in sorted id order (SELECT ... FOR UPDATE) ─────────────
	// Single deterministic query prevents the crossed-lock deadlock where two concurrent
	// transactions acquire the same wallet rows in opposite order.

	if _, err := walletRepo.LockByIDs(ctx, []string{
		sellerBaseWallet.ID,
		buyerBaseWallet.ID,
		buyerQuoteWallet.ID,
		sellerQuoteWallet.ID,
	}); err != nil {
		return fmt.Errorf("failed to acquire deterministic wallet row locks: %w", err)

	}

	// ── Step 7: Leg 1 — Base Asset Transfer (Seller → Buyer) ─────────────────────────────────────

	if isSellerMM {
		if err := walletRepo.DebitAvailable(ctx, sellerBaseWallet.ID, req.BaseAmount); err != nil {
			return fmt.Errorf("failed to debit MM seller available base balance: %w", err)
		}
	} else {
		if err := walletRepo.DebitReserved(ctx, sellerBaseWallet.ID, req.BaseAmount); err != nil {
			return fmt.Errorf("failed to debit seller reserved base balance: %w", err)
		}
		if err := reservRepo.ConsumeRemaining(ctx, sellerRes.ID, req.BaseAmount); err != nil {
			return fmt.Errorf("failed to consume seller reservation: %w", err)
		}
	}
	if err := walletRepo.CreditAvailable(ctx, buyerBaseWallet.ID, req.BaseAmount); err != nil {
		return fmt.Errorf("failed to credit buyer available base balance: %w", err)
	}

	// ── Step 8: Leg 2 — Quote Asset Transfer (Buyer → Seller) ────────────────────────────────────

	if isBuyerMM {
		if err := walletRepo.DebitAvailable(ctx, buyerQuoteWallet.ID, req.QuoteAmount); err != nil {
			return fmt.Errorf("failed to debit MM buyer available quote balance: %w", err)
		}
	} else {
		if err := walletRepo.DebitReserved(ctx, buyerQuoteWallet.ID, req.QuoteAmount); err != nil {
			return fmt.Errorf("failed to debit buyer reserved quote balance: %w", err)
		}
		if err := reservRepo.ConsumeRemaining(ctx, buyerRes.ID, req.QuoteAmount); err != nil {
			return fmt.Errorf("failed to consume buyer reservation: %w", err)
		}
	}
	if err := walletRepo.CreditAvailable(ctx, sellerQuoteWallet.ID, req.QuoteAmount); err != nil {
		return fmt.Errorf("failed to credit seller available quote balance: %w", err)
	}

	now := time.Now().UTC()

	// ── Step 9: Write 4 ledger entries ───────────────────────────────────────────────────────────
	// Seller Base DEBIT | Buyer Base CREDIT | Buyer Quote DEBIT | Seller Quote CREDIT

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
		{ID: sellerBaseTxnID, WalletID: sellerBaseWallet.ID, ReferenceID: req.TradeID, ReferenceType: repository.RefSettlement, TransactionType: repository.TxnTypeDebit, Asset: req.BaseAsset, Amount: req.BaseAmount, CreatedAt: now},
		{ID: buyerBaseTxnID, WalletID: buyerBaseWallet.ID, ReferenceID: req.TradeID, ReferenceType: repository.RefSettlement, TransactionType: repository.TxnTypeCredit, Asset: req.BaseAsset, Amount: req.BaseAmount, CreatedAt: now},
		{ID: buyerQuoteTxnID, WalletID: buyerQuoteWallet.ID, ReferenceID: req.TradeID, ReferenceType: repository.RefSettlement, TransactionType: repository.TxnTypeDebit, Asset: req.QuoteAsset, Amount: req.QuoteAmount, CreatedAt: now},
		{ID: sellerQuoteTxnID, WalletID: sellerQuoteWallet.ID, ReferenceID: req.TradeID, ReferenceType: repository.RefSettlement, TransactionType: repository.TxnTypeCredit, Asset: req.QuoteAsset, Amount: req.QuoteAmount, CreatedAt: now},
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

	// ── Step 10: Write 3 outbox events (see settle_trade_events.go for payload schemas) ──────────
	// TradeSettled → trades.settled.v1 (Trade Service)
	// PortfolioUserTrade BUY  → portfolio.user.trades.v1 (Portfolio Service, buyer partition)
	// PortfolioUserTrade SELL → portfolio.user.trades.v1 (Portfolio Service, seller partition)

	tradeSettledEvent, err := buildTradeSettledEvent(req, now)
	if err != nil {
		return err
	}
	if err := outboxRepo.Insert(ctx, tradeSettledEvent); err != nil {
		return fmt.Errorf("failed to insert TradeSettled outbox event: %w", err)
	}

	buyerPortfolioEvent, err := buildPortfolioEvent(req, req.BuyerUserID, req.BuyOrderID, "BUY", now)
	if err != nil {
		return err
	}
	if err := outboxRepo.Insert(ctx, buyerPortfolioEvent); err != nil {
		return fmt.Errorf("failed to insert buyer PortfolioUserTrade outbox event: %w", err)
	}

	sellerPortfolioEvent, err := buildPortfolioEvent(req, req.SellerUserID, req.SellerOrderID, "SELL", now)
	if err != nil {
		return err
	}
	if err := outboxRepo.Insert(ctx, sellerPortfolioEvent); err != nil {
		return fmt.Errorf("failed to insert seller PortfolioUserTrade outbox event: %w", err)
	}

	// ── Step 11: Commit ───────────────────────────────────────────────────────────────────────────

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
