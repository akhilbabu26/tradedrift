package market

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/matcher"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// Run is the Event Loop goroutine — the ONLY goroutine that touches book.
func (m *MarketEngine) Run(ctx context.Context) {
	lastSnapshotTime := time.Now()
	eventCountSinceLastSnapshot := 0

	// Set defaults if configuration values are not set
	snapshotInterval := m.config.SnapshotInterval
	if snapshotInterval <= 0 {
		snapshotInterval = 10000
	}
	snapshotDuration := m.config.SnapshotDuration
	if snapshotDuration <= 0 {
		snapshotDuration = 60 * time.Second
	}

	for {
		if m.isFatalHalt.Load() {
			log.Printf("[market] fatal halt: stopping event loop immediately for market=%s", m.MarketID)
			close(m.OutputQueue)
			return
		}

		select {
		case event, ok := <-m.InputQueue:
			if !ok {
				m.triggerFinalSnapshot()
				close(m.OutputQueue)
				return
			}

			if event.Type == EventRecoveryBarrier {
				m.OutputQueue <- orderbook.MatchResult{
					DepthSnapshot: orderbook.DepthSnapshot{
						MarketID: m.MarketID,
					},
					BarrierReached: true,
					BarrierOffset:  event.Offset,
					SourcePosition: orderbook.KafkaPosition{
						Topic:     event.Topic,
						Partition: event.Partition,
						Offset:    event.Offset,
					},
				}
				continue
			}

			if event.Offset <= m.lastAppliedOffset {
				continue // skip redelivery
			}

			// INVARIANT (Issue #10): Fail-Closed on logical duplicates.
			if event.EventID != uuid.Nil {
				if m.processedEvents[event.EventID] {
					log.Printf("[market] FATAL: duplicate logical event_id detected (market=%s event_id=%s offset=%d) — fail-closed triggered",
						m.MarketID, event.EventID, event.Offset)
					m.TriggerFatalHalt()
					return
				}
			}

			res, err := m.applyEvent(event)
			if err != nil {
				log.Printf("[market] FATAL mutation failure: %v", err)
				m.TriggerFatalHalt()
				return
			}

			// Logical duplicate deduplication caching (in-memory fast-path)
			if event.EventID != uuid.Nil {
				if evicted := m.eventRing.add(event.EventID); evicted != uuid.Nil {
					delete(m.processedEvents, evicted)
				}
				m.processedEvents[event.EventID] = true
			}

			// Advance offset ONLY after mutation completes successfully (Issue #2)
			m.lastAppliedOffset = event.Offset

			// Check snapshot conditions
			eventCountSinceLastSnapshot++
			isFirstEvent := m.book.Sequence == 1
			timeElapsed := time.Since(lastSnapshotTime) >= snapshotDuration
			countElapsed := eventCountSinceLastSnapshot >= snapshotInterval

			if isFirstEvent || timeElapsed || countElapsed {
				snap := orderbook.Serialize(m.book, m.config.Partition, event.Offset)
				res.Snapshot = &snap
				lastSnapshotTime = time.Now()
				eventCountSinceLastSnapshot = 0
				log.Printf("[market] snapshot generated for market=%s seq=%d offset=%d", m.MarketID, snap.Sequence, snap.Offset)
			}

			if m.mode == ModeLive {
				m.PublishAtomicSnapshot()
			}

			m.OutputQueue <- *res

		case <-ctx.Done():
			if m.isFatalHalt.Load() {
				// FATAL halt path: do NOT drain. Stop immediately.
				// Book state beyond the failure boundary must not be mutated.
				// The checkpoint will be replayed from the last durable position on restart.
				log.Printf("[market] fatal halt: skipping drain for market=%s", m.MarketID)
				close(m.OutputQueue)
				return
			}
			for {
				select {
				case event, ok := <-m.InputQueue:
					if !ok {
						m.triggerFinalSnapshot()
						close(m.OutputQueue)
						return
					}

					if event.Offset <= m.lastAppliedOffset {
						continue
					}

					if event.EventID != uuid.Nil && m.processedEvents[event.EventID] {
						log.Printf("[market] FATAL: duplicate logical event_id detected during shutdown (market=%s event_id=%s offset=%d)",
							m.MarketID, event.EventID, event.Offset)
						m.TriggerFatalHalt()
						return
					}

					res, err := m.applyEvent(event)
					if err != nil {
						log.Printf("[market] FATAL mutation failure during shutdown: %v", err)
						m.TriggerFatalHalt()
						return
					}

					if event.EventID != uuid.Nil {
						if evicted := m.eventRing.add(event.EventID); evicted != uuid.Nil {
							delete(m.processedEvents, evicted)
						}
						m.processedEvents[event.EventID] = true
					}
					m.lastAppliedOffset = event.Offset

					m.OutputQueue <- *res
				default:
					m.triggerFinalSnapshot()
					close(m.OutputQueue)
					return
				}
			}
		}
	}
}

func (m *MarketEngine) triggerFinalSnapshot() {
	if m.lastAppliedOffset >= 0 {
		snap := orderbook.Serialize(m.book, m.config.Partition, m.lastAppliedOffset)
		m.OutputQueue <- orderbook.MatchResult{
			DepthSnapshot: matcher.GetDepth(m.book, 20),
			SourcePosition: orderbook.KafkaPosition{
				Topic:     "orders.commands",
				Partition: m.config.Partition,
				Offset:    m.lastAppliedOffset,
			},
			Snapshot: &snap,
		}
		log.Printf("[market] final shutdown snapshot generated for market=%s seq=%d offset=%d", m.MarketID, snap.Sequence, snap.Offset)
	}
}

func (m *MarketEngine) applyEvent(event InputEvent) (*orderbook.MatchResult, error) {
	matcherMode := matcher.ModeRecovery
	if m.mode == ModeLive {
		matcherMode = matcher.ModeLive
	}

	switch event.Type {
	case EventOrderCreated:
		p := event.OrderCreated
		node := &orderbook.OrderNode{
			OrderID:       p.OrderID,
			UserID:        p.UserID,
			MarketID:      p.MarketID,
			Side:          p.Side,
			OrderType:     p.OrderType,
			Price:         p.Price,
			OriginalQty:   p.Quantity,
			RemainingQty:  p.Quantity,
			Timestamp:     time.Now(),
			ClientOrderID: p.ClientOrderID,
		}

		if m.book.OrderIndex[node.OrderID] != nil {
			dupType := "live-duplicate"
			if m.mode == ModeRecovery {
				dupType = "replay-duplicate"
			}
			log.Printf("[market] [%s] duplicate order_id detected (market=%s order_id=%s) — skipping without state mutation",
				dupType, m.MarketID, node.OrderID)
			return &orderbook.MatchResult{
				DepthSnapshot: matcher.GetDepth(m.book, 20),
				SourcePosition: orderbook.KafkaPosition{
					Topic:     event.Topic,
					Partition: event.Partition,
					Offset:    event.Offset,
				},
			}, nil
		}

		if !validTickAndLot(node, m.config) {
			return &orderbook.MatchResult{
				CancelResult: &orderbook.CancelledOrder{
					OrderID:           node.OrderID,
					UserID:            node.UserID,
					MarketID:          node.MarketID,
					RemainingQuantity: node.OriginalQty,
					Reason:            "invalid_order_parameters",
					CancelledAt:       time.Now(),
					SourceOffset:      event.Offset,
				},
				DepthSnapshot: matcher.GetDepth(m.book, 20),
				SourcePosition: orderbook.KafkaPosition{
					Topic:     event.Topic,
					Partition: event.Partition,
					Offset:    event.Offset,
				},
			}, nil
		}

		fills := matcher.Match(m.book, node, matcherMode, event.EventID)

		var cancel *orderbook.CancelledOrder
		if node.OrderType == orderbook.OrderTypeMarket &&
			node.RemainingQty.GreaterThan(decimal.Zero) {
			cancel = &orderbook.CancelledOrder{
				OrderID:           node.OrderID,
				UserID:            node.UserID,
				MarketID:          node.MarketID,
				RemainingQuantity: node.RemainingQty,
				Reason:            "ioc_expired",
				CancelledAt:       time.Now(),
				SourceOffset:      event.Offset,
			}
		}

		return &orderbook.MatchResult{
			Fills:         fills,
			CancelResult:  cancel,
			DepthSnapshot: matcher.GetDepth(m.book, 20),
			SourcePosition: orderbook.KafkaPosition{
				Topic:     event.Topic,
				Partition: event.Partition,
				Offset:    event.Offset,
			},
		}, nil

	case EventOrderCancel:
		p := event.OrderCancel
		outcome := matcher.Cancel(m.book, p.OrderID)

		if outcome.Status == orderbook.CancelStatusRemovedFromBook {
			// Order found and removed. Increment sequence to maintain monotonic book state.
			m.book.Sequence++
			log.Printf("[cancel] REMOVED_FROM_BOOK market=%s order=%s remaining=%s",
				p.MarketID, outcome.OrderID, outcome.RemainingQuantity.String())
		} else {
			// ALREADY_ABSENT: matcher.Cancel() cannot recover identity
			// because the order is no longer in the book.
			outcome.UserID = p.UserID
			outcome.MarketID = p.MarketID

			log.Printf("[cancel] ALREADY_ABSENT market=%s order=%s user=%s — forwarding for DB resolution",
				p.MarketID, outcome.OrderID, p.UserID)
		}

		// CancelResult is ALWAYS non-nil. The publisher emits OrderCancelOutcome
		// for both statuses so Order Service always receives an authoritative answer.
		return &orderbook.MatchResult{
			CancelResult: &orderbook.CancelledOrder{
				OrderID:           outcome.OrderID,
				UserID:            outcome.UserID,
				MarketID:          outcome.MarketID,
				RemainingQuantity: outcome.RemainingQuantity,
				Reason:            "user_requested",
				CancelStatus:      outcome.Status,
				CancelledAt:       time.Now(),
				SourceOffset:      event.Offset,
			},
			DepthSnapshot: matcher.GetDepth(m.book, 20),
			SourcePosition: orderbook.KafkaPosition{
				Topic:     event.Topic,
				Partition: event.Partition,
				Offset:    event.Offset,
			},
		}, nil

	default:
		return nil, fmt.Errorf("unknown event type: %v", event.Type)
	}
}

func validTickAndLot(node *orderbook.OrderNode, config MarketConfig) bool {
	if node.OrderType == orderbook.OrderTypeLimit {
		if config.TickSize.GreaterThan(decimal.Zero) {
			remainder := node.Price.Mod(config.TickSize)
			if !remainder.IsZero() {
				return false
			}
		}
	}
	if config.LotSize.GreaterThan(decimal.Zero) {
		remainder := node.RemainingQty.Mod(config.LotSize)
		if !remainder.IsZero() {
			return false
		}
	}
	return true
}
