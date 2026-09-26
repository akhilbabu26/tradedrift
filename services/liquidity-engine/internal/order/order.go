package order

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/pricing"
)

// Status represents the lifecycle state of a tracked MM order.
type Status string

const (
	// StatusPending — OrderCreate published to Kafka, awaiting OS confirmation.
	// The LE has sent the command but has not yet verified the OS received it.
	// Diff: excluded from CREATE.
	StatusPending Status = "PENDING"

	// StatusOSRegistered — Order Service has confirmed the order exists (OPEN).
	// This means OS will return it on ListMMOrders for recovery.
	// It does NOT mean ME has the order in the live order book.
	// Diff: excluded from CREATE (already in flight).
	// Transitions to RESTING after MEConfirmationTimeout if ME is healthy.
	StatusOSRegistered Status = "OS_REGISTERED"

	// StatusResting — ME has accepted the order into the live order book.
	// Confirmed indirectly: OS is OPEN and ME liveness healthy for MEConfirmationTimeout.
	// In V2, an OrderRested Kafka event from ME will trigger this directly.
	// Diff: eligible for CANCEL or CORRECT.
	StatusResting Status = "RESTING"

	// StatusCancelling — OrderCancel published, awaiting Order Service confirmation.
	// Diff: excluded from all actions (let it resolve).
	StatusCancelling Status = "CANCELLING"

	// StatusStale — Cancel retry limit exceeded, reconciliation frozen for this level.
	// Diff: excluded from all actions. Authoritative resync required.
	StatusStale Status = "STALE"
)

// LiveOrder is one entry in the tracker.
// It represents the LE's current working knowledge of a specific MM order.
//
// Three-layer identity:
//
//	LevelID        = "MM-BTC-USDT-ASK-01"          (stable logical slot)
//	Generation     = 3                               (monotonic lifecycle counter)
//	ClientOrderID  = "MM-BTC-USDT-ASK-01-G003"     (idempotency key sent to Order Service)
//	OrderID        = "<UUID assigned by ME/OS>"     (authoritative ID for cancel commands)
type LiveOrder struct {
	// Identity
	LevelID       string // stable logical slot (never changes)
	Generation    int    // monotonic; increments on fill/correction/completion
	ClientOrderID string // LE-generated idempotency key = LevelID + "-G" + zero-padded generation
	OrderID       string // ME/OS-assigned UUID (learned from ListMMOrders, used in cancel payload)

	// Order details
	MarketID     string
	Side         string
	Price        decimal.Decimal
	OriginalQty  decimal.Decimal // from Order Service response
	RemainingQty decimal.Decimal // from Order Service response — used for committed calc
	FilledQty    decimal.Decimal // derived: OriginalQty - RemainingQty

	// State
	Status Status

	// Kafka dispatch tracking (Fix 4)
	KafkaPublished bool // true once successfully written to Kafka topic

	// Timing and retry tracking
	PendingSince      time.Time
	OSRegisteredSince time.Time // set when status transitions PENDING → OS_REGISTERED
	CancellingSince   time.Time
	CancelRetries     int

	// CORRECT flow: set when a CANCEL is issued to correct a wrong-price order.
	// After cancel confirms, the reconciler creates a replacement using this level.
	QueuedCorrection *pricing.PriceLevel
}

// IncrementCancelRetry increments the retry counter and resets the timer.
// Called from the CANCELLING timeout handler inside the single event loop.
func (o *LiveOrder) IncrementCancelRetry() {
	o.CancelRetries++
	o.CancellingSince = time.Now()
}

// OSOrder is the minimal order representation received from the Order Service.
// The LevelID and Generation are extracted from the idempotency_key field (= client_order_id).
type OSOrder struct {
	LevelID       string          // extracted from ClientOrderID (prefix before last "-G")
	Generation    int             // extracted from ClientOrderID (suffix after last "-G")
	ClientOrderID string          // = idempotency_key from Order Service
	OrderID       string          // ME/OS-assigned UUID
	Side          string
	Price         decimal.Decimal
	OriginalQty   decimal.Decimal // = order.Quantity
	RemainingQty  decimal.Decimal // = order.RemainingQuantity
}

// ClientOrderID constructs the client_order_id for a given level and generation.
// Format: "MM-BTC-USDT-ASK-01-G003"
func ClientOrderID(levelID string, gen int) string {
	return fmt.Sprintf("%s-G%03d", levelID, gen)
}
