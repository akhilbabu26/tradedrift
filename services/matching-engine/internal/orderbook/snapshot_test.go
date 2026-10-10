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

func TestRestore_OriginalQty_LotSizeViolation_Fails(t *testing.T) {
	book := orderbook.NewOrderBook("BTC-USDT")

	o := orderbook.SnapshotOrder{
		OrderID:      uuid.New().String(),
		UserID:       uuid.New().String(),
		Side:         "BUY",
		OrderType:    "LIMIT",
		Price:        "65000.00",
		OriginalQty:  "1.005", // off-lot original quantity (lotSize 0.01)
		RemainingQty: "1.00",  // compliant remaining quantity
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
		t.Fatal("expected restore to fail for OriginalQty lot size violation")
	}
}

func TestRestore_FutureTimestamp_Fails(t *testing.T) {
	book := orderbook.NewOrderBook("BTC-USDT")

	o := orderbook.SnapshotOrder{
		OrderID:      uuid.New().String(),
		UserID:       uuid.New().String(),
		Side:         "BUY",
		OrderType:    "LIMIT",
		Price:        "65000.00",
		OriginalQty:  "1.00",
		RemainingQty: "1.00",
		Timestamp:    time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339Nano), // 2 days in future
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

	err := orderbook.Restore(book, snap, "BTC-USDT", 0, 100, checksum, decimal.RequireFromString("0.01"), decimal.RequireFromString("0.01"))
	if err == nil {
		t.Fatal("expected restore to fail for far-future timestamp")
	}
}

func TestRestore_StructuralOrder_BidsAscending_Fails(t *testing.T) {
	book := orderbook.NewOrderBook("BTC-USDT")
	now := time.Now().UTC()

	// Corrupt snapshot: bids sorted ascending instead of descending
	bids := []orderbook.SnapshotOrder{
		{
			OrderID:      uuid.New().String(),
			UserID:       uuid.New().String(),
			Side:         "BUY",
			OrderType:    "LIMIT",
			Price:        "64000.00",
			OriginalQty:  "1.00",
			RemainingQty: "1.00",
			Timestamp:    now.Format(time.RFC3339Nano),
		},
		{
			OrderID:      uuid.New().String(),
			UserID:       uuid.New().String(),
			Side:         "BUY",
			OrderType:    "LIMIT",
			Price:        "65000.00", // HIGHER than previous bid -> violates descending invariant!
			OriginalQty:  "1.00",
			RemainingQty: "1.00",
			Timestamp:    now.Add(time.Millisecond).Format(time.RFC3339Nano),
		},
	}

	snap := orderbook.BookSnapshot{
		SchemaVersion: 1,
		MarketID:      "BTC-USDT",
		Partition:     0,
		Sequence:      10,
		Offset:        100,
		Orders:        bids,
	}

	snapJSON, _ := json.Marshal(snap)
	h := sha256.New()
	h.Write(snapJSON)
	checksum := h.Sum(nil)

	err := orderbook.Restore(book, snap, "BTC-USDT", 0, 100, checksum, decimal.RequireFromString("0.01"), decimal.RequireFromString("0.01"))
	if err == nil {
		t.Fatal("expected restore to fail for non-descending bid price order")
	}
}

func TestRestore_StructuralOrder_BuyAfterSell_Fails(t *testing.T) {
	book := orderbook.NewOrderBook("BTC-USDT")
	now := time.Now().UTC()

	// Corrupt snapshot: buy order placed AFTER sell order
	orders := []orderbook.SnapshotOrder{
		{
			OrderID:      uuid.New().String(),
			UserID:       uuid.New().String(),
			Side:         "SELL",
			OrderType:    "LIMIT",
			Price:        "66000.00",
			OriginalQty:  "1.00",
			RemainingQty: "1.00",
			Timestamp:    now.Format(time.RFC3339Nano),
		},
		{
			OrderID:      uuid.New().String(),
			UserID:       uuid.New().String(),
			Side:         "BUY", // buy after sell!
			OrderType:    "LIMIT",
			Price:        "65000.00",
			OriginalQty:  "1.00",
			RemainingQty: "1.00",
			Timestamp:    now.Add(time.Millisecond).Format(time.RFC3339Nano),
		},
	}

	snap := orderbook.BookSnapshot{
		SchemaVersion: 1,
		MarketID:      "BTC-USDT",
		Partition:     0,
		Sequence:      10,
		Offset:        100,
		Orders:        orders,
	}

	snapJSON, _ := json.Marshal(snap)
	h := sha256.New()
	h.Write(snapJSON)
	checksum := h.Sum(nil)

	err := orderbook.Restore(book, snap, "BTC-USDT", 0, 100, checksum, decimal.RequireFromString("0.01"), decimal.RequireFromString("0.01"))
	if err == nil {
		t.Fatal("expected restore to fail when BUY appears after SELL")
	}
}

func TestRestore_InvalidSnapshot_PreservesExistingBook(t *testing.T) {
	book := orderbook.NewOrderBook("BTC-USDT")
	existingOrderID := uuid.New()
	existingUserID := uuid.New()
	existingNode := &orderbook.OrderNode{
		OrderID:      existingOrderID,
		UserID:       existingUserID,
		MarketID:     "BTC-USDT",
		Side:         orderbook.SideBuy,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString("60000.00"),
		OriginalQty:  decimal.RequireFromString("5.00"),
		RemainingQty: decimal.RequireFromString("5.00"),
		Timestamp:    time.Now().UTC(),
	}
	orderbook.InsertRestoredOrder(book, existingNode)
	book.Sequence = 42

	// Snapshot with order 1 valid and order 2 invalid (non-conforming lot size)
	now := time.Now().UTC()
	orders := []orderbook.SnapshotOrder{
		{
			OrderID:      uuid.New().String(),
			UserID:       uuid.New().String(),
			Side:         "BUY",
			OrderType:    "LIMIT",
			Price:        "65000.00",
			OriginalQty:  "1.00",
			RemainingQty: "1.00",
			Timestamp:    now.Format(time.RFC3339Nano),
		},
		{
			OrderID:      uuid.New().String(),
			UserID:       uuid.New().String(),
			Side:         "BUY",
			OrderType:    "LIMIT",
			Price:        "64000.00",
			OriginalQty:  "1.00",
			RemainingQty: "0.003", // violates lot size 0.01!
			Timestamp:    now.Add(time.Millisecond).Format(time.RFC3339Nano),
		},
	}

	snap := orderbook.BookSnapshot{
		SchemaVersion: 1,
		MarketID:      "BTC-USDT",
		Partition:     0,
		Sequence:      999,
		Offset:        500,
		Orders:        orders,
	}

	snapJSON, _ := json.Marshal(snap)
	h := sha256.New()
	h.Write(snapJSON)
	checksum := h.Sum(nil)

	err := orderbook.Restore(book, snap, "BTC-USDT", 0, 500, checksum, decimal.RequireFromString("0.01"), decimal.RequireFromString("0.01"))
	if err == nil {
		t.Fatal("expected restore to fail due to lot size violation in snapshot")
	}

	// Transactional invariant: Live book MUST remain completely uncorrupted
	if book.Sequence != 42 {
		t.Fatalf("expected sequence to remain 42, got %d", book.Sequence)
	}
	if len(book.OrderIndex) != 1 {
		t.Fatalf("expected exactly 1 order in index, got %d", len(book.OrderIndex))
	}
	preservedNode := book.OrderIndex[existingOrderID]
	if preservedNode == nil {
		t.Fatal("expected existing order to still exist in index")
	}
	if preservedNode.RemainingQty.String() != "5" {
		t.Fatalf("expected preserved node qty 5, got %s", preservedNode.RemainingQty.String())
	}
	level := book.Bids.PriceLevels[existingNode.Price.String()]
	if level == nil || !level.TotalQty.Equal(decimal.RequireFromString("5")) {
		t.Fatalf("expected preserved price level total qty 5, got %v", level)
	}
}

