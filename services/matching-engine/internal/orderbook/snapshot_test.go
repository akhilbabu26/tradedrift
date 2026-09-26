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

// TestSerialize_ClientOrderID_RoundTrip verifies that ClientOrderID is persisted
// through Serialize → Restore so ME restarts preserve MM order identity (Fix 2).
func TestSerialize_ClientOrderID_RoundTrip(t *testing.T) {
	book := orderbook.NewOrderBook("SOL-USDT")

	clientOID := "MM-SOL-USDT-BID-01-G004"
	node := &orderbook.OrderNode{
		OrderID:       uuid.New(),
		UserID:        uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		MarketID:      "SOL-USDT",
		ClientOrderID: clientOID,
		Side:          orderbook.SideBuy,
		OrderType:     orderbook.OrderTypeLimit,
		Price:         decimal.RequireFromString("100.00"),
		OriginalQty:   decimal.RequireFromString("1.00"),
		RemainingQty:  decimal.RequireFromString("1.00"),
		Timestamp:     time.Now().UTC(),
	}
	orderbook.InsertRestoredOrder(book, node)

	// Serialize
	snap := orderbook.Serialize(book, 0, 42)
	if len(snap.Orders) != 1 {
		t.Fatalf("expected 1 snapshot order, got %d", len(snap.Orders))
	}
	if snap.Orders[0].ClientOrderID != clientOID {
		t.Errorf("Serialize: expected ClientOrderID=%q, got %q", clientOID, snap.Orders[0].ClientOrderID)
	}

	// Restore into fresh book
	checksum, err := orderbook.Checksum(snap)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}
	book2 := orderbook.NewOrderBook("SOL-USDT")
	tickSize := decimal.RequireFromString("0.01")
	lotSize := decimal.RequireFromString("0.01")
	if err := orderbook.Restore(book2, snap, "SOL-USDT", 0, 42, checksum, tickSize, lotSize); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(book2.OrderIndex) != 1 {
		t.Fatalf("expected 1 restored order, got %d", len(book2.OrderIndex))
	}
	for _, restored := range book2.OrderIndex {
		if restored.ClientOrderID != clientOID {
			t.Errorf("Restore: expected ClientOrderID=%q, got %q", clientOID, restored.ClientOrderID)
		}
	}
}

// TestRestore_LegacySnapshot_NoClientOrderID verifies T16: snapshots written before
// the ClientOrderID field was added still restore correctly. The field defaults to ""
// which is identical to pre-fix behaviour — identity falls back to OrderID matching.
func TestRestore_LegacySnapshot_NoClientOrderID(t *testing.T) {
	book := orderbook.NewOrderBook("BTC-USDT")

	// Simulate a legacy JSON snapshot without "client_order_id"
	legacyJSON := `{
		"schema_version": 1,
		"market_id": "BTC-USDT",
		"partition": 0,
		"offset": 50,
		"sequence": 5,
		"orders": [{
			"order_id": "` + uuid.New().String() + `",
			"user_id": "` + uuid.New().String() + `",
			"side": "BUY",
			"order_type": "LIMIT",
			"price": "60000.00",
			"original_qty": "1.00",
			"remaining_qty": "1.00",
			"timestamp": "` + time.Now().UTC().Format(time.RFC3339Nano) + `"
		}]
	}`

	var snap orderbook.BookSnapshot
	if err := json.Unmarshal([]byte(legacyJSON), &snap); err != nil {
		t.Fatalf("unmarshal legacy snapshot: %v", err)
	}

	// Checksum over the unmarshalled struct — same as real restore path
	checksum, err := orderbook.Checksum(snap)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}

	tickSize := decimal.RequireFromString("0.01")
	lotSize := decimal.RequireFromString("0.01")
	if err := orderbook.Restore(book, snap, "BTC-USDT", 0, 50, checksum, tickSize, lotSize); err != nil {
		t.Fatalf("expected legacy snapshot to restore cleanly, got: %v", err)
	}

	if len(book.OrderIndex) != 1 {
		t.Fatalf("expected 1 restored order, got %d", len(book.OrderIndex))
	}
	// ClientOrderID must be empty string — caller falls back to OrderID matching
	for _, restored := range book.OrderIndex {
		if restored.ClientOrderID != "" {
			t.Errorf("expected empty ClientOrderID for legacy snapshot, got %q", restored.ClientOrderID)
		}
	}
}
