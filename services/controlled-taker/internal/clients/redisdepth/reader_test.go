package redisdepth

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// mockRedisGetter implements the minimal parsing test without a live Redis instance.
func parseSnapshotJSON(marketID string, jsonStr string) (*DepthSnapshot, error) {
	var dto depthSnapshotDTO
	if err := json.Unmarshal([]byte(jsonStr), &dto); err != nil {
		return nil, err
	}

	// 1. Validate Market ID
	if dto.MarketID != marketID {
		return nil, ErrMarketMismatch
	}

	// 2. Validate SnapshotAt Timestamp
	if dto.SnapshotAt == "" {
		return nil, ErrEmptySnapshotAt
	}

	snapshotTime, parseErr := time.Parse(time.RFC3339Nano, dto.SnapshotAt)
	if parseErr != nil || snapshotTime.IsZero() {
		snapshotTime, parseErr = time.Parse(time.RFC3339, dto.SnapshotAt)
		if parseErr != nil || snapshotTime.IsZero() {
			return nil, ErrEmptySnapshotAt
		}
	}

	now := time.Now()
	if now.Sub(snapshotTime) > 5*time.Second {
		return nil, ErrStaleSnapshot
	}
	if snapshotTime.Sub(now) > 1*time.Second {
		return nil, ErrFutureSnapshot
	}

	// 3. Validate Presence of Levels
	if len(dto.Bids) == 0 || len(dto.Asks) == 0 {
		return nil, ErrIncompleteDepth
	}

	// 4. Parse & Validate Bids (Strictly Descending, Price > 0, Qty > 0)
	bids := make([]DepthLevel, 0, len(dto.Bids))
	for i, b := range dto.Bids {
		p, errP := decimal.NewFromString(b.Price)
		q, errQ := decimal.NewFromString(b.Quantity)
		if errP != nil || errQ != nil || !p.GreaterThan(decimal.Zero) || !q.GreaterThan(decimal.Zero) {
			return nil, ErrInvalidOrderBook
		}
		if i > 0 && !bids[i-1].Price.GreaterThan(p) {
			return nil, ErrInvalidOrderBook
		}
		bids = append(bids, DepthLevel{Price: p, Quantity: q})
	}

	// 5. Parse & Validate Asks (Strictly Ascending, Price > 0, Qty > 0)
	asks := make([]DepthLevel, 0, len(dto.Asks))
	for i, a := range dto.Asks {
		p, errP := decimal.NewFromString(a.Price)
		q, errQ := decimal.NewFromString(a.Quantity)
		if errP != nil || errQ != nil || !p.GreaterThan(decimal.Zero) || !q.GreaterThan(decimal.Zero) {
			return nil, ErrInvalidOrderBook
		}
		if i > 0 && !p.GreaterThan(asks[i-1].Price) {
			return nil, ErrInvalidOrderBook
		}
		asks = append(asks, DepthLevel{Price: p, Quantity: q})
	}

	// 6. Crossed/Locked Book Validation
	if !asks[0].Price.GreaterThan(bids[0].Price) {
		return nil, ErrCrossedOrderBook
	}

	return &DepthSnapshot{
		MarketID:   dto.MarketID,
		Sequence:   dto.Sequence,
		Bids:       bids,
		Asks:       asks,
		SnapshotAt: snapshotTime,
	}, nil
}

func TestDepthValidation_MarketMismatch(t *testing.T) {
	raw := `{"market_id":"ETH-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err := parseSnapshotJSON("BTC-USDT", raw)
	if !errors.Is(err, ErrMarketMismatch) {
		t.Fatalf("expected ErrMarketMismatch, got %v", err)
	}
}

func TestDepthValidation_MissingOrMalformedTimestamp(t *testing.T) {
	rawNoTime := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err := parseSnapshotJSON("BTC-USDT", rawNoTime)
	if !errors.Is(err, ErrEmptySnapshotAt) {
		t.Fatalf("expected ErrEmptySnapshotAt for empty timestamp, got %v", err)
	}

	rawGarbageTime := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"garbage-timestamp","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err = parseSnapshotJSON("BTC-USDT", rawGarbageTime)
	if !errors.Is(err, ErrEmptySnapshotAt) {
		t.Fatalf("expected ErrEmptySnapshotAt for garbage timestamp, got %v", err)
	}
}

func TestDepthValidation_StaleOrFutureTimestamp(t *testing.T) {
	rawStale := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Add(-10*time.Second).Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err := parseSnapshotJSON("BTC-USDT", rawStale)
	if !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("expected ErrStaleSnapshot, got %v", err)
	}

	rawFuture := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Add(10*time.Second).Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err = parseSnapshotJSON("BTC-USDT", rawFuture)
	if !errors.Is(err, ErrFutureSnapshot) {
		t.Fatalf("expected ErrFutureSnapshot, got %v", err)
	}
}

func TestDepthValidation_IncompleteDepth(t *testing.T) {
	rawEmptyBids := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err := parseSnapshotJSON("BTC-USDT", rawEmptyBids)
	if !errors.Is(err, ErrIncompleteDepth) {
		t.Fatalf("expected ErrIncompleteDepth for empty bids, got %v", err)
	}
}

func TestDepthValidation_InvalidOrderBookOrdering(t *testing.T) {
	// Bids not strictly descending: 96480 then 96490
	rawBadBids := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[{"price":"96480","quantity":"1.0"},{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err := parseSnapshotJSON("BTC-USDT", rawBadBids)
	if !errors.Is(err, ErrInvalidOrderBook) {
		t.Fatalf("expected ErrInvalidOrderBook for non-descending bids, got %v", err)
	}

	// Asks not strictly ascending: 96520 then 96510
	rawBadAsks := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96520","quantity":"1.0"},{"price":"96510","quantity":"1.0"}]}`
	_, err = parseSnapshotJSON("BTC-USDT", rawBadAsks)
	if !errors.Is(err, ErrInvalidOrderBook) {
		t.Fatalf("expected ErrInvalidOrderBook for non-ascending asks, got %v", err)
	}
}

func TestDepthValidation_CrossedOrLockedBook(t *testing.T) {
	// Crossed: best ask 96480 <= best bid 96490
	rawCrossed := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96480","quantity":"1.0"}]}`
	_, err := parseSnapshotJSON("BTC-USDT", rawCrossed)
	if !errors.Is(err, ErrCrossedOrderBook) {
		t.Fatalf("expected ErrCrossedOrderBook, got %v", err)
	}
}

func TestDepthValidation_ValidSnapshot(t *testing.T) {
	rawValid := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"},{"price":"96480","quantity":"2.0"}],"asks":[{"price":"96500","quantity":"1.0"},{"price":"96510","quantity":"3.0"}]}`
	snap, err := parseSnapshotJSON("BTC-USDT", rawValid)
	if err != nil {
		t.Fatalf("expected valid snapshot to succeed, got %v", err)
	}
	if snap.MarketID != "BTC-USDT" || len(snap.Bids) != 2 || len(snap.Asks) != 2 {
		t.Fatalf("unexpected snapshot contents: %+v", snap)
	}
}
