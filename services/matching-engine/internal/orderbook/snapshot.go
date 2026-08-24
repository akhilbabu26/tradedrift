package orderbook

import (
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const CurrentSchemaVersion = 1

var (
	ErrSnapshotMarketMismatch    = errors.New("snapshot market mismatch")
	ErrSnapshotPartitionMismatch = errors.New("snapshot partition mismatch")
	ErrSnapshotBeyondCheckpoint  = errors.New("snapshot offset is beyond partition checkpoint")
	ErrSnapshotChecksumMismatch  = errors.New("snapshot checksum validation failed")
	ErrSnapshotSchemaMismatch    = errors.New("unsupported snapshot schema version")
)

type BookSnapshot struct {
	SchemaVersion uint32          `json:"schema_version"`
	MarketID      string          `json:"market_id"`
	Partition     int             `json:"partition"`
	Offset        int64           `json:"offset"` // Kafka offset at snapshot time
	Sequence      uint64          `json:"sequence"`
	Orders        []SnapshotOrder `json:"orders"`
}

type SnapshotOrder struct {
	OrderID      string `json:"order_id"`
	UserID       string `json:"user_id"`
	Side         string `json:"side"`
	OrderType    string `json:"order_type"`
	Price        string `json:"price"`
	OriginalQty  string `json:"original_qty"`
	RemainingQty string `json:"remaining_qty"`
	Timestamp    string `json:"timestamp"` // RFC3339Nano
}

type SnapshotRecord struct {
	Snapshot BookSnapshot
	Checksum []byte
}

// Checksum calculates SHA-256 over deterministic Go struct JSON serialization
func Checksum(snap BookSnapshot) ([]byte, error) {
	data, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("marshal for checksum: %w", err)
	}
	hash := sha256.Sum256(data)
	return hash[:], nil
}

// Serialize converts an OrderBook to a BookSnapshot in a deterministic price-time format
func Serialize(book *OrderBook, partition int, offset int64) BookSnapshot {
	snap := BookSnapshot{
		SchemaVersion: CurrentSchemaVersion,
		MarketID:      book.MarketID,
		Partition:     partition,
		Offset:        offset,
		Sequence:      book.Sequence,
		Orders:        make([]SnapshotOrder, 0, len(book.OrderIndex)),
	}

	// 1. Serialize Bids (sorted price descending)
	for _, price := range book.Bids.SortedPrices {
		level := book.Bids.PriceLevels[price.String()]
		if level != nil {
			for elem := level.Orders.Front(); elem != nil; elem = elem.Next() {
				node := elem.Value.(*OrderNode)
				snap.Orders = append(snap.Orders, SnapshotOrder{
					OrderID:      node.OrderID.String(),
					UserID:       node.UserID.String(),
					Side:         string(node.Side),
					OrderType:    string(node.OrderType),
					Price:        node.Price.String(),
					OriginalQty:  node.OriginalQty.String(),
					RemainingQty: node.RemainingQty.String(),
					Timestamp:    node.Timestamp.Format(time.RFC3339Nano),
				})
			}
		}
	}

	// 2. Serialize Asks (sorted price ascending)
	for _, price := range book.Asks.SortedPrices {
		level := book.Asks.PriceLevels[price.String()]
		if level != nil {
			for elem := level.Orders.Front(); elem != nil; elem = elem.Next() {
				node := elem.Value.(*OrderNode)
				snap.Orders = append(snap.Orders, SnapshotOrder{
					OrderID:      node.OrderID.String(),
					UserID:       node.UserID.String(),
					Side:         string(node.Side),
					OrderType:    string(node.OrderType),
					Price:        node.Price.String(),
					OriginalQty:  node.OriginalQty.String(),
					RemainingQty: node.RemainingQty.String(),
					Timestamp:    node.Timestamp.Format(time.RFC3339Nano),
				})
			}
		}
	}

	return snap
}

// Restore validates the snapshot metadata and reconstructs the OrderBook
func Restore(
	book *OrderBook,
	snap BookSnapshot,
	marketID string,
	partition int,
	checkpoint int64,
	expectedChecksum []byte,
	tickSize decimal.Decimal,
	lotSize decimal.Decimal,
) error {
	if snap.SchemaVersion != CurrentSchemaVersion {
		return ErrSnapshotSchemaMismatch
	}
	if snap.MarketID != marketID {
		return ErrSnapshotMarketMismatch
	}
	if snap.Partition != partition {
		return ErrSnapshotPartitionMismatch
	}
	if snap.Offset > checkpoint {
		return ErrSnapshotBeyondCheckpoint
	}

	computed, err := Checksum(snap)
	if err != nil {
		return fmt.Errorf("calculate snapshot checksum: %w", err)
	}
	if string(computed) != string(expectedChecksum) {
		return ErrSnapshotChecksumMismatch
	}

	// Reset book structures
	book.Bids = Side{
		IsBid:        true,
		SortedPrices: make([]decimal.Decimal, 0),
		PriceLevels:  make(map[string]*PriceLevel),
	}
	book.Asks = Side{
		IsBid:        false,
		SortedPrices: make([]decimal.Decimal, 0),
		PriceLevels:  make(map[string]*PriceLevel),
	}
	book.OrderIndex = make(map[uuid.UUID]*OrderNode)
	book.Sequence = snap.Sequence

	// Replay orders in serialized FIFO order
	for _, o := range snap.Orders {
		oID, err := uuid.Parse(o.OrderID)
		if err != nil {
			return fmt.Errorf("invalid order_id: %w", err)
		}
		uID, err := uuid.Parse(o.UserID)
		if err != nil {
			return fmt.Errorf("invalid user_id: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, o.Timestamp)
		if err != nil {
			return fmt.Errorf("invalid timestamp: %w", err)
		}
		price, err := decimal.NewFromString(o.Price)
		if err != nil {
			return fmt.Errorf("invalid snapshot price %q: %w", o.Price, err)
		}
		origQty, err := decimal.NewFromString(o.OriginalQty)
		if err != nil {
			return fmt.Errorf("invalid snapshot original quantity %q: %w", o.OriginalQty, err)
		}
		remQty, err := decimal.NewFromString(o.RemainingQty)
		if err != nil {
			return fmt.Errorf("invalid snapshot remaining quantity %q: %w", o.RemainingQty, err)
		}

		// Validation checks (Issue #8 & v9.4 Patches)
		if SideType(o.Side) != SideBuy && SideType(o.Side) != SideSell {
			return fmt.Errorf("invalid snapshot side %q", o.Side)
		}
		if OrderType(o.OrderType) != OrderTypeLimit {
			return fmt.Errorf("invalid snapshot: order type %q not supported for resting order in book", o.OrderType)
		}
		if origQty.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("invalid snapshot original qty %s: must be > 0", origQty)
		}
		if remQty.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("invalid snapshot remaining qty %s: must be > 0 for resting order", remQty)
		}
		if remQty.GreaterThan(origQty) {
			return fmt.Errorf("invalid snapshot remaining qty %s cannot exceed original qty %s", remQty, origQty)
		}
		if !price.Mod(tickSize).IsZero() {
			return fmt.Errorf("invalid snapshot order price %s: does not conform to tick size %s", price, tickSize)
		}
		if !remQty.Mod(lotSize).IsZero() {
			return fmt.Errorf("invalid snapshot order remaining qty %s: does not conform to lot size %s", remQty, lotSize)
		}
		if OrderType(o.OrderType) == OrderTypeLimit && price.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("invalid snapshot limit price %s: must be > 0", price)
		}
		if _, exists := book.OrderIndex[oID]; exists {
			return fmt.Errorf("duplicate order_id %s in snapshot", oID)
		}

		node := &OrderNode{
			OrderID:      oID,
			UserID:       uID,
			MarketID:     marketID,
			Side:         SideType(o.Side),
			OrderType:    OrderType(o.OrderType),
			Price:        price,
			OriginalQty:  origQty,
			RemainingQty: remQty,
			Timestamp:    t,
		}
		InsertRestoredOrder(book, node)
	}

	return nil
}

// InsertRestoredOrder inserts order directly into structures without sequence modifications or side effects
func InsertRestoredOrder(book *OrderBook, node *OrderNode) {
	var side *Side
	if node.Side == SideBuy {
		side = &book.Bids
	} else {
		side = &book.Asks
	}
	priceKey := node.Price.String()
	level := side.PriceLevels[priceKey]
	if level == nil {
		level = &PriceLevel{
			Price:  node.Price,
			Orders: list.New(),
		}
		side.PriceLevels[priceKey] = level

		// Insert into SortedPrices slice preserving price order
		idx := binarySearchInsertIndex(side, node.Price)
		side.SortedPrices = insertAt(side.SortedPrices, idx, node.Price)
	}
	node.Element = level.Orders.PushBack(node)
	level.TotalQty = level.TotalQty.Add(node.RemainingQty)
	book.OrderIndex[node.OrderID] = node
}

// Replicate binarySearchInsertIndex and insertAt logic to avoid import cycles or dependency on internal/matcher
func binarySearchInsertIndex(side *Side, price decimal.Decimal) int {
	// Simple lookup (if bids: descending, if asks: ascending)
	for i, p := range side.SortedPrices {
		if side.IsBid {
			if p.LessThan(price) {
				return i
			}
		} else {
			if p.GreaterThan(price) {
				return i
			}
		}
	}
	return len(side.SortedPrices)
}

func insertAt(prices []decimal.Decimal, idx int, price decimal.Decimal) []decimal.Decimal {
	prices = append(prices, decimal.Zero)
	copy(prices[idx+1:], prices[idx:])
	prices[idx] = price
	return prices
}
