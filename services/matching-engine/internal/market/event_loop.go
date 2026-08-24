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
					if m.HaltCallback != nil {
						m.HaltCallback()
					}
					return
				}
			}

			res, err := m.applyEvent(event)
			if err != nil {
				log.Printf("[market] FATAL mutation failure: %v", err)
				if m.HaltCallback != nil {
					m.HaltCallback()
				}
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

			m.OutputQueue <- *res

		case <-ctx.Done():
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
						if m.HaltCallback != nil {
							m.HaltCallback()
						}
						return
					}

					res, err := m.applyEvent(event)
					if err != nil {
						log.Printf("[market] FATAL mutation failure during shutdown: %v", err)
						if m.HaltCallback != nil {
							m.HaltCallback()
						}
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
			OrderID:      p.OrderID,
			UserID:       p.UserID,
			MarketID:     p.MarketID,
			Side:         p.Side,
			OrderType:    p.OrderType,
			Price:        p.Price,
			OriginalQty:  p.Quantity,
			RemainingQty: p.Quantity,
			Timestamp:    time.Now(),
		}

		if m.book.OrderIndex[node.OrderID] != nil {
			log.Printf("[market] duplicate order_id detected (market=%s order_id=%s) — skipping without state mutation",
				m.MarketID, node.OrderID)
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
		node := matcher.Cancel(m.book, p.OrderID)

		var cancel *orderbook.CancelledOrder
		if node != nil {
			m.book.Sequence++
			cancel = &orderbook.CancelledOrder{
				OrderID:           node.OrderID,
				UserID:            node.UserID,
				MarketID:          node.MarketID,
				RemainingQuantity: node.RemainingQty,
				Reason:            "user_requested",
				CancelledAt:       time.Now(),
			}
		}

		return &orderbook.MatchResult{
			CancelResult:  cancel,
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
