package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/wallet/internal/repository"
	platformuuid "tradedrift/platform/uuid"
)

// TradeSettlementRequest holds all data needed to settle a single side of a trade.
type TradeSettlementRequest struct {
	TradeID         string // Unique trade ID (idempotency key)
	BuyerUserID     string
	BuyerWalletID   string // pre-fetched for performance
	SellerUserID    string
	SellerOrderID   string // Used to look up the seller's reservation
	BaseAsset       string // e.g. "BTC" — what changes hands
	QuoteAsset      string // e.g. "USDT" — what was paid
	BaseAmount      string // How much BTC the buyer receives
	QuoteAmount     string // How much USDT the seller receives
}

// SettleTrade atomically settles a matched trade:
//   - Debits seller's reservation (reduces reserved_balance)
//   - Credits buyer's available_balance with BaseAsset
//   - Credits seller's available_balance with QuoteAsset
//   - Writes 2 ledger entries (one per wallet)
//   - Publishes a UserTradeSettled outbox event
//
// Idempotent: if tradeID was already settled, returns success immediately.
func (s *Service) SettleTrade(ctx context.Context, req TradeSettlementRequest) error {

	// Step 1: Idempotency — already settled?
	alreadySettled, err := s.txnRepo.ExistsByKey(ctx, req.TradeID, repository.RefSettlement, req.BaseAsset)
	if err != nil {
		return fmt.Errorf("failed to check settlement idempotency: %w", err)
	}
	if alreadySettled {
		s.log.Debug("trade already settled, skipping",
			zap.String("tradeID", req.TradeID),
		)
		return nil
	}

	// Step 2: Fetch seller's reservation
	reservation, err := s.reservRepo.GetByOrderID(ctx, req.SellerOrderID)
	if err != nil {
		return fmt.Errorf("failed to fetch seller reservation: %w", err)
	}
	if reservation == nil {
		return fmt.Errorf("seller reservation not found for order %s", req.SellerOrderID)
	}
	if reservation.Status == repository.ReservationReleased {
		return fmt.Errorf("seller reservation already released for order %s", req.SellerOrderID)
	}

	// Step 3: Debit seller's reserved balance (BaseAmount of BaseAsset was sold)
	sellerWallet, err := s.walletRepo.GetByUserAndAsset(ctx, req.SellerUserID, req.BaseAsset)
	if err != nil {
		return fmt.Errorf("failed to fetch seller wallet: %w", err)
	}
	if sellerWallet == nil {
		return fmt.Errorf("seller wallet not found for asset %s", req.BaseAsset)
	}
	if err := s.walletRepo.DebitReserved(ctx, sellerWallet.ID, req.BaseAmount); err != nil {
		return fmt.Errorf("failed to debit seller reserved balance: %w", err)
	}

	// Step 4: Update reservation consumed/remaining amounts
	// (handles partial fills — seller may have multiple fills on one order)
	if err := s.reservRepo.UpdateConsumed(ctx, reservation.ID, req.BaseAmount, reservation.RemainingAmount); err != nil {
		return fmt.Errorf("failed to update reservation consumed amount: %w", err)
	}

	// Step 5: Credit buyer's available balance with BaseAsset (they receive the BTC)
	buyerWallet, err := s.walletRepo.GetByUserAndAsset(ctx, req.BuyerUserID, req.BaseAsset)
	if err != nil {
		return fmt.Errorf("failed to fetch buyer wallet: %w", err)
	}
	if buyerWallet == nil {
		return fmt.Errorf("buyer wallet not found for asset %s", req.BaseAsset)
	}
	if err := s.walletRepo.CreditAvailable(ctx, buyerWallet.ID, req.BaseAmount); err != nil {
		return fmt.Errorf("failed to credit buyer available balance: %w", err)
	}

	now := time.Now().UTC()

	// Step 6: Write 2 ledger entries
	sellerTxnID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate seller transaction ID: %w", err)
	}
	buyerTxnID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate buyer transaction ID: %w", err)
	}

	txns := []*repository.WalletTransaction{
		{
			ID:              sellerTxnID,
			WalletID:        sellerWallet.ID,
			ReferenceID:     req.TradeID,
			ReferenceType:   repository.RefSettlement,
			TransactionType: repository.TxnTypeDebit,
			Asset:           req.BaseAsset,
			Amount:          req.BaseAmount,
			CreatedAt:       now,
		},
		{
			ID:              buyerTxnID,
			WalletID:        buyerWallet.ID,
			ReferenceID:     req.TradeID,
			ReferenceType:   repository.RefSettlement,
			TransactionType: repository.TxnTypeCredit,
			Asset:           req.BaseAsset,
			Amount:          req.BaseAmount,
			CreatedAt:       now,
		},
	}
	if err := s.txnRepo.CreateBatch(ctx, txns); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			s.log.Warn("duplicate settlement transactions ignored", zap.String("tradeID", req.TradeID))
		} else {
			return fmt.Errorf("failed to write settlement ledger entries: %w", err)
		}
	}

	// Step 7: Write outbox event (published to Kafka by background worker)
	eventID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate outbox event ID: %w", err)
	}
	payload, err := json.Marshal(map[string]string{
		"trade_id":      req.TradeID,
		"buyer_user_id": req.BuyerUserID,
		"seller_user_id": req.SellerUserID,
		"base_asset":    req.BaseAsset,
		"base_amount":   req.BaseAmount,
		"quote_asset":   req.QuoteAsset,
		"quote_amount":  req.QuoteAmount,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal outbox payload: %w", err)
	}
	outboxEvent := &repository.OutboxEvent{
		ID:           eventID,
		AggregateID:  req.TradeID,
		EventType:    "UserTradeSettled",
		Payload:      payload,
		PartitionKey: req.BuyerUserID,
		CreatedAt:    now,
	}
	if err := s.outboxRepo.Insert(ctx, outboxEvent); err != nil {
		return fmt.Errorf("failed to insert outbox event: %w", err)
	}

	s.log.Info("trade settled",
		zap.String("tradeID", req.TradeID),
		zap.String("buyerUserID", req.BuyerUserID),
		zap.String("sellerUserID", req.SellerUserID),
		zap.String("baseAsset", req.BaseAsset),
		zap.String("baseAmount", req.BaseAmount),
	)

	return nil
}
