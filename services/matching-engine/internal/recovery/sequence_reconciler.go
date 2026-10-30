package recovery

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// DownstreamSequenceEvidence contains sequence observations from downstream services.
type DownstreamSequenceEvidence struct {
	TradeSeq      uint64
	PortfolioSeq  uint64
	SettlementSeq uint64
	SnapshotSeq   uint64
	InFlightCount int
}

// DownstreamSequenceReconciler reconciles authoritative sequence across trade, portfolio,
// settlement, and snapshot sources when market_sequences is missing.
type DownstreamSequenceReconciler interface {
	ReconcileSequence(ctx context.Context, marketID string) (uint64, error)
}

// DefaultSequenceReconciler inspects local database snapshots and available downstream sources.
type DefaultSequenceReconciler struct {
	db pgxConn
}

// NewDefaultSequenceReconciler creates a new DefaultSequenceReconciler.
func NewDefaultSequenceReconciler(db pgxConn) *DefaultSequenceReconciler {
	return &DefaultSequenceReconciler{db: db}
}

// ReconcileSequence returns the latest snapshot or recorded market sequence from local database.
func (r *DefaultSequenceReconciler) ReconcileSequence(ctx context.Context, marketID string) (uint64, error) {
	var snapSeq uint64
	err := r.db.QueryRow(ctx, "SELECT COALESCE(MAX(sequence), 0) FROM market_snapshots WHERE market_id = $1", marketID).Scan(&snapSeq)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("query snapshot sequence for %s: %w", marketID, err)
	}

	var dbSeq uint64
	err = r.db.QueryRow(ctx, "SELECT COALESCE(sequence, 0) FROM market_sequences WHERE market_id = $1", marketID).Scan(&dbSeq)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("query market sequence for %s: %w", marketID, err)
	}

	if dbSeq > snapSeq {
		return dbSeq, nil
	}
	return snapSeq, nil
}

// RuleBasedSequenceReconciler reconciles multi-source downstream evidence and enforces
// fail-closed semantics when sequence sources conflict or in-flight events exist.
type RuleBasedSequenceReconciler struct {
	EvidenceSource func(ctx context.Context, marketID string) (DownstreamSequenceEvidence, error)
}

// ReconcileSequence reconciles downstream sequence evidence and fails closed on inconsistency.
func (r *RuleBasedSequenceReconciler) ReconcileSequence(ctx context.Context, marketID string) (uint64, error) {
	if r.EvidenceSource == nil {
		return 0, fmt.Errorf("evidence source not configured for market %s", marketID)
	}
	ev, err := r.EvidenceSource(ctx, marketID)
	if err != nil {
		return 0, fmt.Errorf("fetch sequence evidence for market %s: %w", marketID, err)
	}

	// 1. Unresolved in-flight events make the baseline uncertain -> fail closed
	if ev.InFlightCount > 0 {
		return 0, fmt.Errorf("fail closed: %d unresolved in-flight events detected for market %s; baseline uncertain", ev.InFlightCount, marketID)
	}

	// 2. Conflict detection across trade, portfolio, and settlement history
	if ev.TradeSeq > 0 {
		if ev.PortfolioSeq > ev.TradeSeq {
			return 0, fmt.Errorf("fail closed: sequence conflict for market %s: portfolio sequence %d > trade sequence %d", marketID, ev.PortfolioSeq, ev.TradeSeq)
		}
		if ev.SettlementSeq > ev.TradeSeq {
			return 0, fmt.Errorf("fail closed: sequence conflict for market %s: settlement sequence %d > trade sequence %d", marketID, ev.SettlementSeq, ev.TradeSeq)
		}
		if ev.SnapshotSeq > ev.TradeSeq {
			return 0, fmt.Errorf("fail closed: sequence conflict for market %s: snapshot sequence %d > trade sequence %d", marketID, ev.SnapshotSeq, ev.TradeSeq)
		}
		if ev.PortfolioSeq > 0 && ev.SettlementSeq > 0 {
			if ev.PortfolioSeq != ev.TradeSeq || ev.SettlementSeq != ev.TradeSeq {
				return 0, fmt.Errorf("fail closed: downstream sequence divergence for market %s: trade=%d, portfolio=%d, settlement=%d",
					marketID, ev.TradeSeq, ev.PortfolioSeq, ev.SettlementSeq)
			}
		}
		return ev.TradeSeq, nil
	}

	if ev.PortfolioSeq > 0 || ev.SettlementSeq > 0 {
		return 0, fmt.Errorf("fail closed: inconsistent sequence evidence for market %s: trade=0 but portfolio=%d, settlement=%d",
			marketID, ev.PortfolioSeq, ev.SettlementSeq)
	}

	if ev.SnapshotSeq > 0 {
		return ev.SnapshotSeq, nil
	}

	return 0, nil
}
