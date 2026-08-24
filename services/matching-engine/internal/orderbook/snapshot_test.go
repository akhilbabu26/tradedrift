package orderbook_test

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/orderbook"
)

func TestRestore_ValidSnapshot(t *testing.T) {
	book := orderbook.NewOrderBook("BTC-USDT")

	o := orderbook.SnapshotOrder{
		OrderID:      uuid.New().String(),
		UserID:       uuid.New().String(),
		Side:         "BUY",
		OrderType:    "LIMIT",
		Price:        "65000.00",
		OriginalQty:  "1.50",
		RemainingQty: "1.00",
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	snap := orderbook.BookSnapshot{
		SchemaVersion: 1,
		MarketID:      "BTC-USDT",
		Partition:     0,
		Sequence:      10,
		Offset:        100,
		Orders:        []orderbook.SnapshotOrder{o},
	}

	snapJSON, _ := json.Marshal(snap)
	h := sha256.New()
	h.Write(snapJSON)
	checksum := h.Sum(nil)

	tickSize := decimal.RequireFromString("0.01")
	lotSize := decimal.RequireFromString("0.01")

	err := orderbook.Restore(book, snap, "BTC-USDT", 0, 100, checksum, tickSize, lotSize)
	if err != nil {
		t.Fatalf("expected successful snapshot restore, got: %v", err)
	}

	if book.Sequence != 10 {
		t.Errorf("expected sequence 10, got %d", book.Sequence)
	}
	if len(book.OrderIndex) != 1 {
		t.Errorf("expected 1 order, got %d", len(book.OrderIndex))
	}
}

func TestRestore_MarketOrderResting_Fails(t *testing.T) {
	book := orderbook.NewOrderBook("BTC-USDT")

	o := orderbook.SnapshotOrder{
		OrderID:      uuid.New().String(),
		UserID:       uuid.New().String(),
		Side:         "BUY",
		OrderType:    "MARKET", // Resting MARKET order is impossible
		Price:        "65000.00",
		OriginalQty:  "1.50",
		RemainingQty: "1.00",
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	snap := orderbook.BookSnapshot{
		SchemaVersion: 1,
		MarketID:      "BTC-USDT",
		Partition:     0,
		Sequence:      10,
		Offset:        100,
		Orders:        []orderbook.SnapshotOrder{o},
	}

	snapJSON, _ := json.Marshal(snap)
	h := sha256.New()
	h.Write(snapJSON)
	checksum := h.Sum(nil)

	tickSize := decimal.RequireFromString("0.01")
	lotSize := decimal.RequireFromString("0.01")

	err := orderbook.Restore(book, snap, "BTC-USDT", 0, 100, checksum, tickSize, lotSize)
	if err == nil {
		t.Fatal("expected restore to fail for resting MARKET order")
	}
}

func TestRestore_TickSizeViolation_Fails(t *testing.T) {
	book := orderbook.NewOrderBook("BTC-USDT")

	o := orderbook.SnapshotOrder{
		OrderID:      uuid.New().String(),
		UserID:       uuid.New().String(),
		Side:         "BUY",
		OrderType:    "LIMIT",
		Price:        "65000.005", // violating tickSize of 0.01
		OriginalQty:  "1.50",
		RemainingQty: "1.00",
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	snap := orderbook.BookSnapshot{
		SchemaVersion: 1,
		MarketID:      "BTC-USDT",
		Partition:     0,
		Sequence:      10,
		Offset:        100,
		Orders:        []orderbook.SnapshotOrder{o},
	}

	snapJSON, _ := json.Marshal(snap)
	h := sha256.New()
	h.Write(snapJSON)
	checksum := h.Sum(nil)

	tickSize := decimal.RequireFromString("0.01")
	lotSize := decimal.RequireFromString("0.01")

	err := orderbook.Restore(book, snap, "BTC-USDT", 0, 100, checksum, tickSize, lotSize)
	if err == nil {
		t.Fatal("expected restore to fail for tick size violation")
	}
}

func TestRestore_LotSizeViolation_Fails(t *testing.T) {
	book := orderbook.NewOrderBook("BTC-USDT")

	o := orderbook.SnapshotOrder{
		OrderID:      uuid.New().String(),
		UserID:       uuid.New().String(),
		Side:         "BUY",
		OrderType:    "LIMIT",
		Price:        "65000.00",
		OriginalQty:  "1.50",
		RemainingQty: "1.005", // violating lotSize of 0.01
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	snap := orderbook.BookSnapshot{
		SchemaVersion: 1,
		MarketID:      "BTC-USDT",
		Partition:     0,
		Sequence:      10,
		Offset:        100,
		Orders:        []orderbook.SnapshotOrder{o},
	}

	snapJSON, _ := json.Marshal(snap)
	h := sha256.New()
	h.Write(snapJSON)
	checksum := h.Sum(nil)

	tickSize := decimal.RequireFromString("0.01")
	lotSize := decimal.RequireFromString("0.01")

	err := orderbook.Restore(book, snap, "BTC-USDT", 0, 100, checksum, tickSize, lotSize)
	if err == nil {
		t.Fatal("expected restore to fail for lot size violation")
	}
}
