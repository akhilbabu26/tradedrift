package market

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var MMUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

type MMOrderSummary struct {
	OrderID           string `json:"order_id"`
	ClientOrderID     string `json:"client_order_id,omitempty"`
	LevelID           string `json:"level_id,omitempty"`
	Generation        uint64 `json:"generation,omitempty"`
	Side              string `json:"side"`
	Price             string `json:"price"`
	RemainingQuantity string `json:"remaining_quantity"`
}

type MarketSnapshot struct {
	MarketID   string           `json:"market_id"`
	State      string           `json:"state"`       // "LIVE" | "RECOVERING"
	Sequence   uint64           `json:"sequence"`    // Monotonically increasing sequence
	SnapshotAt time.Time        `json:"snapshot_at"` // Coherent point-in-time timestamp
	Orders     []MMOrderSummary `json:"orders"`      // null when RECOVERING; [] when empty; list when resting
}

func parseMMClientOrderID(coid string) (levelID string, gen uint64) {
	if coid == "" {
		return "", 0
	}
	parts := strings.Split(coid, "-G")
	if len(parts) == 2 {
		levelID = parts[0]
		g, err := strconv.ParseUint(parts[1], 10, 64)
		if err == nil {
			gen = g
			return
		}
	}
	return coid, 0
}

// GetSnapshot returns the current atomic immutable snapshot of this market.
func (m *MarketEngine) GetSnapshot() *MarketSnapshot {
	return m.snapshot.Load()
}

// PublishAtomicSnapshot builds an immutable snapshot from the current book state and stores it atomically.
// Safe to call from the single-threaded event loop or before Run().
func (m *MarketEngine) PublishAtomicSnapshot() {
	state := "RECOVERING"
	if m.mode == ModeLive {
		state = "LIVE"
	}

	if m.mode != ModeLive {
		m.snapshot.Store(&MarketSnapshot{
			MarketID:   m.MarketID,
			State:      state,
			Sequence:   m.book.Sequence,
			SnapshotAt: time.Now().UTC(),
			Orders:     nil,
		})
		return
	}

	mmOrders := make([]MMOrderSummary, 0, len(m.book.OrderIndex))
	for _, node := range m.book.OrderIndex {
		if node.UserID == MMUserID {
			lvl, gen := parseMMClientOrderID(node.ClientOrderID)
			mmOrders = append(mmOrders, MMOrderSummary{
				OrderID:           node.OrderID.String(),
				ClientOrderID:     node.ClientOrderID,
				LevelID:           lvl,
				Generation:        gen,
				Side:              string(node.Side),
				Price:             node.Price.String(),
				RemainingQuantity: node.RemainingQty.String(),
			})
		}
	}

	sort.Slice(mmOrders, func(i, j int) bool {
		if mmOrders[i].LevelID != mmOrders[j].LevelID {
			return mmOrders[i].LevelID < mmOrders[j].LevelID
		}
		if mmOrders[i].Generation != mmOrders[j].Generation {
			return mmOrders[i].Generation < mmOrders[j].Generation
		}
		return mmOrders[i].OrderID < mmOrders[j].OrderID
	})

	m.snapshot.Store(&MarketSnapshot{
		MarketID:   m.MarketID,
		State:      state,
		Sequence:   m.book.Sequence,
		SnapshotAt: time.Now().UTC(),
		Orders:     mmOrders,
	})
}
 