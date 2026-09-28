package test

import (
	"errors"
	"testing"
	"time"

	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
)

func TestDepthValidation_MarketMismatch(t *testing.T) {
	raw := `{"market_id":"ETH-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err := redisdepth.ParseSnapshotJSON("BTC-USDT", []byte(raw))
	if !errors.Is(err, redisdepth.ErrMarketMismatch) {
		t.Fatalf("expected ErrMarketMismatch, got %v", err)
	}
}

func TestDepthValidation_MissingOrMalformedTimestamp(t *testing.T) {
	rawNoTime := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err := redisdepth.ParseSnapshotJSON("BTC-USDT", []byte(rawNoTime))
	if !errors.Is(err, redisdepth.ErrEmptySnapshotAt) {
		t.Fatalf("expected ErrEmptySnapshotAt for empty timestamp, got %v", err)
	}

	rawGarbageTime := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"garbage-timestamp","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err = redisdepth.ParseSnapshotJSON("BTC-USDT", []byte(rawGarbageTime))
	if !errors.Is(err, redisdepth.ErrEmptySnapshotAt) {
		t.Fatalf("expected ErrEmptySnapshotAt for garbage timestamp, got %v", err)
	}
}

func TestDepthValidation_StaleOrFutureTimestamp(t *testing.T) {
	rawStale := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Add(-10*time.Second).Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err := redisdepth.ParseSnapshotJSON("BTC-USDT", []byte(rawStale))
	if !errors.Is(err, redisdepth.ErrStaleSnapshot) {
		t.Fatalf("expected ErrStaleSnapshot, got %v", err)
	}

	rawFuture := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Add(10*time.Second).Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err = redisdepth.ParseSnapshotJSON("BTC-USDT", []byte(rawFuture))
	if !errors.Is(err, redisdepth.ErrFutureSnapshot) {
		t.Fatalf("expected ErrFutureSnapshot, got %v", err)
	}
}

func TestDepthValidation_IncompleteDepth(t *testing.T) {
	rawEmptyBids := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err := redisdepth.ParseSnapshotJSON("BTC-USDT", []byte(rawEmptyBids))
	if !errors.Is(err, redisdepth.ErrIncompleteDepth) {
		t.Fatalf("expected ErrIncompleteDepth for empty bids, got %v", err)
	}
}

func TestDepthValidation_InvalidOrderBookOrdering(t *testing.T) {
	// Bids not strictly descending: 96480 then 96490
	rawBadBids := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[{"price":"96480","quantity":"1.0"},{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96500","quantity":"1.0"}]}`
	_, err := redisdepth.ParseSnapshotJSON("BTC-USDT", []byte(rawBadBids))
	if !errors.Is(err, redisdepth.ErrInvalidOrderBook) {
		t.Fatalf("expected ErrInvalidOrderBook for non-descending bids, got %v", err)
	}

	// Asks not strictly ascending: 96520 then 96510
	rawBadAsks := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96520","quantity":"1.0"},{"price":"96510","quantity":"1.0"}]}`
	_, err = redisdepth.ParseSnapshotJSON("BTC-USDT", []byte(rawBadAsks))
	if !errors.Is(err, redisdepth.ErrInvalidOrderBook) {
		t.Fatalf("expected ErrInvalidOrderBook for non-ascending asks, got %v", err)
	}
}

func TestDepthValidation_CrossedOrLockedBook(t *testing.T) {
	// Crossed: best ask 96480 <= best bid 96490
	rawCrossed := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"}],"asks":[{"price":"96480","quantity":"1.0"}]}`
	_, err := redisdepth.ParseSnapshotJSON("BTC-USDT", []byte(rawCrossed))
	if !errors.Is(err, redisdepth.ErrCrossedOrderBook) {
		t.Fatalf("expected ErrCrossedOrderBook, got %v", err)
	}
}

func TestDepthValidation_ValidSnapshot(t *testing.T) {
	rawValid := `{"market_id":"BTC-USDT","sequence":100,"snapshot_at":"` + time.Now().Format(time.RFC3339) + `","bids":[{"price":"96490","quantity":"1.0"},{"price":"96480","quantity":"2.0"}],"asks":[{"price":"96500","quantity":"1.0"},{"price":"96510","quantity":"3.0"}]}`
	snap, err := redisdepth.ParseSnapshotJSON("BTC-USDT", []byte(rawValid))
	if err != nil {
		t.Fatalf("expected valid snapshot to succeed, got %v", err)
	}
	if snap.MarketID != "BTC-USDT" || len(snap.Bids) != 2 || len(snap.Asks) != 2 {
		t.Fatalf("unexpected snapshot contents: %+v", snap)
	}
}
