package service

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"tradedrift/services/wallet/internal/repository"
)

// maxDecimalScale is the maximum number of decimal places accepted for all financial amounts.
// Matches the PostgreSQL column definition DECIMAL(30,10) and Portfolio Service's invariant.
const maxDecimalScale = 10

// validateSettlementAmounts performs full decimal parsing and cross-field financial invariant checks.
// This runs before any database work so that malformed or manipulated requests are rejected at the
// financial boundary — Wallet is the final arbiter of what moves between accounts.
//
// Invariants enforced:
//   - BaseAmount, QuoteAmount, Price, Quantity must parse as valid decimals (rejects "abc", "1e5", " 1 ", ".")
//   - All four must be > 0
//   - Scientific notation is rejected (e.g. "5e4", "1E10") — plain decimal notation only
//   - Decimal scale must be <= maxDecimalScale (matches DECIMAL(30,10) column)
//   - BaseAmount == Quantity (quantity of base asset the buyer receives)
//   - QuoteAmount == Price × Quantity (within quote asset precision rounding tolerance)
//   - MarketID == BaseAsset + "-" + QuoteAsset (platform market ID convention)
func validateSettlementAmounts(ctx context.Context, assetRepo repository.AssetRepository, req TradeSettlementRequest) error {
	parse := func(field, value string) (decimal.Decimal, error) {
		d, err := decimal.NewFromString(value)
		if err != nil {
			return decimal.Zero, fmt.Errorf("%w: %s %q is not a valid decimal: %v", repository.ErrInvalidSettlement, field, value, err)
		}
		if !d.IsPositive() {
			return decimal.Zero, fmt.Errorf("%w: %s must be > 0, got %q", repository.ErrInvalidSettlement, field, value)
		}
		// Reject scientific notation (e.g. "5e4", "1E10") — all amounts must be plain decimal.
		// Exponent() > 0 means the value uses a positive exponent (scientific notation).
		if d.Exponent() > 0 {
			return decimal.Zero, fmt.Errorf("%w: %s %q must be plain decimal notation (scientific notation not accepted)", repository.ErrInvalidSettlement, field, value)
		}
		// Exponent() returns negative of decimal places (e.g. "1.123" → -3).
		// Scale = -Exponent() when Exponent < 0; 0 otherwise.
		scale := -int(d.Exponent())
		if scale < 0 {
			scale = 0
		}
		if scale > maxDecimalScale {
			return decimal.Zero, fmt.Errorf("%w: %s has %d decimal places, maximum is %d", repository.ErrInvalidSettlement, field, scale, maxDecimalScale)
		}
		return d, nil
	}

	baseAmount, err := parse("base_amount", req.BaseAmount)
	if err != nil {
		return err
	}
	quoteAmount, err := parse("quote_amount", req.QuoteAmount)
	if err != nil {
		return err
	}
	price, err := parse("price", req.Price)
	if err != nil {
		return err
	}
	quantity, err := parse("quantity", req.Quantity)
	if err != nil {
		return err
	}

	// BaseAmount must equal Quantity — the quantity of the base asset changing hands.
	if !baseAmount.Equal(quantity) {
		return fmt.Errorf("%w: base_amount %s must equal quantity %s", repository.ErrInvalidSettlement, req.BaseAmount, req.Quantity)
	}

	// QuoteAmount must be within precision tolerance of Price × Quantity.
	// After floor-rounding to the quote asset's precision (e.g. USDT=2dp), the computed
	// quoteAmount may differ from exact price×qty by at most one unit in the least significant
	// decimal place (e.g. 0.01 for USDT).
	expectedQuote := price.Mul(quantity)
	quoteAsset, err := assetRepo.GetByCode(ctx, req.QuoteAsset)
	if err != nil {
		return fmt.Errorf("failed to lookup quote asset %s: %w", req.QuoteAsset, err)
	}
	if quoteAsset == nil {
		return fmt.Errorf("%w: quote asset %s is not supported", repository.ErrInvalidSettlement, req.QuoteAsset)
	}
	tolerance := decimal.New(1, -int32(quoteAsset.Decimals))
	diff := quoteAmount.Sub(expectedQuote).Abs()
	if diff.GreaterThan(tolerance) {
		return fmt.Errorf("%w: quote_amount %s does not match price(%s) × quantity(%s) = %s (diff %s exceeds tolerance %s)",
			repository.ErrInvalidSettlement, req.QuoteAmount, req.Price, req.Quantity, expectedQuote.String(), diff.String(), tolerance.String())
	}

	// MarketID must be BaseAsset + "-" + QuoteAsset.
	expectedMarketID := req.BaseAsset + "-" + req.QuoteAsset
	if req.MarketID != expectedMarketID {
		return fmt.Errorf("%w: market_id %q does not match base_asset %q + quote_asset %q (expected %q)",
			repository.ErrInvalidSettlement, req.MarketID, req.BaseAsset, req.QuoteAsset, expectedMarketID)
	}

	return nil
}

// validateAssetPrecision checks that the given amount does not exceed the per-asset decimal
// precision configured in supported_assets.decimals.
// This supplements the global maxDecimalScale cap with a tighter asset-specific constraint
// (e.g. USDT=2, BTC=8, SOL=9).
func validateAssetPrecision(ctx context.Context, assetRepo repository.AssetRepository, assetCode, field, amount string) error {
	assetInfo, err := assetRepo.GetByCode(ctx, assetCode)
	if err != nil {
		return fmt.Errorf("failed to look up asset %s for precision check: %w", assetCode, err)
	}
	if assetInfo == nil {
		return fmt.Errorf("%w: asset %q is not a supported asset", repository.ErrInvalidSettlement, assetCode)
	}
	if !assetInfo.IsEnabled {
		return fmt.Errorf("%w: asset %q is currently disabled", repository.ErrInvalidSettlement, assetCode)
	}
	d, err := decimal.NewFromString(amount)
	if err != nil {
		// Already validated upstream; this is a safeguard.
		return fmt.Errorf("%w: %s %q is not a valid decimal", repository.ErrInvalidSettlement, field, amount)
	}
	scale := decimalScale(d)
	if scale > assetInfo.Decimals {
		return fmt.Errorf("%w: %s %s has %d decimal places, maximum for %s is %d",
			repository.ErrInvalidSettlement, field, amount, scale, assetCode, assetInfo.Decimals)
	}
	return nil
}

// roundToAssetPrecision rounds the given decimal string DOWN (floor) to the asset's
// declared decimal precision. This is used for computed values like price × quantity
// where floating-point multiplication can produce more decimal places than the asset allows.
// Floor-rounding is the correct financial behaviour: the buyer pays the smaller rounded amount.
func roundToAssetPrecision(ctx context.Context, assetRepo repository.AssetRepository, assetCode, amount string) (string, error) {
	assetInfo, err := assetRepo.GetByCode(ctx, assetCode)
	if err != nil {
		return "", fmt.Errorf("failed to look up asset %s for precision rounding: %w", assetCode, err)
	}
	if assetInfo == nil {
		return "", fmt.Errorf("asset %q is not a supported asset", assetCode)
	}
	d, err := decimal.NewFromString(amount)
	if err != nil {
		return "", fmt.Errorf("invalid decimal %q: %w", amount, err)
	}
	// Floor to asset precision (truncate extra decimal places, do not round up).
	rounded := d.Truncate(int32(assetInfo.Decimals))
	return rounded.String(), nil
}

