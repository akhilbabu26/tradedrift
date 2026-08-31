// Package order provides the Diff() algorithm — the core of the reconciliation loop.
// Diff compares desired MM ladder state against actual tracker state and produces
// a minimal set of actions (CREATE / CANCEL / CORRECT) to converge them.
//
// Key invariants:
//  1. If Desired == Actual → entries is empty → ZERO Kafka commands.
//  2. PENDING / CANCELLING / STALE levels are excluded from all diff actions.
//  3. CORRECT = Cancel + Create (V1 has no atomic OrderReplace).
//  4. Price comparison uses decimal.Equal() — tick-rounded prices are exact integers.
//  5. Only RESTING orders are eligible for CANCEL or CORRECT.
package order

import (
	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// DiffAction represents the type of reconciliation command.
type DiffAction int

const (
	DiffCreate DiffAction = iota // level_id desired but not in knownSet
	DiffCancel                   // level_id is RESTING but not in desired
	DiffCorrect                  // level_id is RESTING but price is wrong or qty depleted
)

// DiffEntry is a single reconciliation command produced by Diff().
type DiffEntry struct {
	Action       DiffAction
	LevelID      string
	DesiredLevel *pricing.PriceLevel // set for CREATE and CORRECT
	ExistingCOID string             // client_order_id for CANCEL and CORRECT
	ExistingOID  string             // order_id (ME UUID) for CANCEL and CORRECT
}

// Diff compares desired ladder levels against actual tracked orders for a market.
// Returns the minimal set of DiffEntry actions needed to converge actual → desired.
//
// The two diff passes:
//  1. For each desired level:
//     - if not in knownSet → CREATE
//     - if RESTING with wrong price or exhausted qty → CORRECT
//  2. For each RESTING order not in desired → CANCEL
//
// PENDING, CANCELLING, and STALE orders are in the knownSet but are excluded from
// CREATE (to prevent duplicates) and also from CANCEL/CORRECT (let them resolve first).
func Diff(desired []pricing.PriceLevel, tracker *Tracker, marketID string, cfg *config.MarketConfig) []DiffEntry {
	// Build knownSet from tracker: PENDING, RESTING, CANCELLING, STALE all block CREATE
	actual := tracker.All(marketID)

	knownSet := make(map[string]*LiveOrder, len(actual))
	restingSet := make(map[string]*LiveOrder)

	for _, o := range actual {
		knownSet[o.LevelID] = o
		if o.Status == StatusResting {
			restingSet[o.LevelID] = o
		}
	}

	desiredSet := make(map[string]*pricing.PriceLevel, len(desired))
	for i := range desired {
		dl := &desired[i]
		desiredSet[dl.LevelID] = dl
	}

	var entries []DiffEntry

	// Pass 1: desired vs known → CREATE or CORRECT
	for levelID, dl := range desiredSet {
		existing, inKnown := knownSet[levelID]

		if !inKnown {
			// Level is desired but not in any known state → CREATE
			entries = append(entries, DiffEntry{
				Action:       DiffCreate,
				LevelID:      levelID,
				DesiredLevel: dl,
			})
			continue
		}

		// Only RESTING orders can be corrected — PENDING/CANCELLING/STALE are left alone
		if existing.Status != StatusResting {
			continue
		}

		// Price mismatch check: use Equal() — tick-rounded prices are exact decimal integers
		if !existing.Price.Equal(dl.Price) {
			entries = append(entries, DiffEntry{
				Action:       DiffCorrect,
				LevelID:      levelID,
				DesiredLevel: dl,
				ExistingCOID: existing.ClientOrderID,
				ExistingOID:  existing.OrderID,
			})
			continue
		}

		// Quantity effectively consumed: remaining below MinOrderSize
		if existing.RemainingQty.LessThan(cfg.MinOrderSize) {
			entries = append(entries, DiffEntry{
				Action:       DiffCorrect,
				LevelID:      levelID,
				DesiredLevel: dl,
				ExistingCOID: existing.ClientOrderID,
				ExistingOID:  existing.OrderID,
			})
			continue
		}

		// Price matches, quantity adequate → KEEP (no entry = no Kafka command)
	}

	// Pass 2: RESTING orders not in desired → CANCEL
	for levelID, o := range restingSet {
		if _, inDesired := desiredSet[levelID]; !inDesired {
			entries = append(entries, DiffEntry{
				Action:       DiffCancel,
				LevelID:      levelID,
				ExistingCOID: o.ClientOrderID,
				ExistingOID:  o.OrderID,
			})
		}
	}

	return entries
}
