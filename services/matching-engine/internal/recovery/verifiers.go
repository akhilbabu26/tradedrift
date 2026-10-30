package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// EmptyManifestChecksum is the SHA-256 hash of an empty manifest payload.
const EmptyManifestChecksum = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// ComputeCanonicalManifestChecksum computes a deterministic SHA-256 digest of the
// market's authoritative order and trade inventory.
func ComputeCanonicalManifestChecksum(marketID string, totalOrders int64, buyFills, sellFills string) string {
	if totalOrders == 0 {
		return EmptyManifestChecksum
	}
	payload := fmt.Sprintf("market:%s|orders:%d|buy:%s|sell:%s", marketID, totalOrders, buyFills, sellFills)
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:])
}

// PostgresOrderVerifier queries the authoritative Order Service orders table
// to assert that no active nonterminal orders remain and that financial volume invariants hold.
type PostgresOrderVerifier struct {
	db pgxConn
}

// NewPostgresOrderVerifier constructs a new PostgresOrderVerifier.
func NewPostgresOrderVerifier(db pgxConn) *PostgresOrderVerifier {
	return &PostgresOrderVerifier{db: db}
}

// VerifyOrdersReconciled queries the orders table. Fails closed on any database error,
// if any nonterminal orders exist, if order volume invariants are violated,
// or if the canonical manifest checksum does not match the authoritative state.
func (v *PostgresOrderVerifier) VerifyOrdersReconciled(ctx context.Context, marketID string, manifestChecksum string) error {
	if v == nil || v.db == nil {
		return errors.New("order verifier database connection is nil")
	}
	if marketID == "" {
		return errors.New("order verifier: market_id is required")
	}
	if manifestChecksum == "" {
		return errors.New("order verifier: manifest_checksum is required")
	}
	if len(manifestChecksum) != 64 || !isHex(manifestChecksum) {
		return fmt.Errorf("order verifier: invalid manifest_checksum format: %s", manifestChecksum)
	}

	// 1. Assert zero active nonterminal orders remain
	var activeOrdersCount int64
	const checkOrdersSQL = `SELECT COUNT(*) FROM orders WHERE market_id = $1 AND status IN ('OPEN', 'PARTIALLY_FILLED', 'CANCELLING')`
	err := v.db.QueryRow(ctx, checkOrdersSQL, marketID).Scan(&activeOrdersCount)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query active orders for market %s: %w", marketID, err)
		}
	} else if activeOrdersCount > 0 {
		return fmt.Errorf("incomplete reconciliation: Order Service still has %d active nonterminal orders for market %s",
			activeOrdersCount, marketID)
	}

	// 2. Assert order volume integrity: quantity == filled_quantity + remaining_quantity
	var brokenOrdersCount int64
	const checkQtySQL = `SELECT COUNT(*) FROM orders WHERE market_id = $1 AND quantity != (filled_quantity + remaining_quantity)`
	err = v.db.QueryRow(ctx, checkQtySQL, marketID).Scan(&brokenOrdersCount)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query order quantity integrity for market %s: %w", marketID, err)
		}
	} else if brokenOrdersCount > 0 {
		return fmt.Errorf("order quantity invariant violation: %d orders have quantity != filled_quantity + remaining_quantity for market %s",
			brokenOrdersCount, marketID)
	}

	// 3. Financial invariant check: BUY filled volume MUST equal SELL filled volume
	var totalOrdersCount int64
	const countTotalSQL = `SELECT COUNT(*) FROM orders WHERE market_id = $1`
	err = v.db.QueryRow(ctx, countTotalSQL, marketID).Scan(&totalOrdersCount)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("query total orders for market %s: %w", marketID, err)
	}

	var buyFills, sellFills string
	const checkFillsSQL = `SELECT COALESCE(SUM(CASE WHEN side = 'BUY' THEN filled_quantity ELSE 0 END), 0)::text, COALESCE(SUM(CASE WHEN side = 'SELL' THEN filled_quantity ELSE 0 END), 0)::text FROM orders WHERE market_id = $1`
	err = v.db.QueryRow(ctx, checkFillsSQL, marketID).Scan(&buyFills, &sellFills)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("query fill totals for market %s: %w", marketID, err)
	}
	if buyFills != "" && sellFills != "" && buyFills != sellFills {
		return fmt.Errorf("financial invariant violation: total BUY filled quantity (%s) does not match total SELL filled quantity (%s) for market %s",
			buyFills, sellFills, marketID)
	}

	// 4. Canonical manifest checksum verification
	canonicalChecksum := ComputeCanonicalManifestChecksum(marketID, totalOrdersCount, buyFills, sellFills)
	if !strings.EqualFold(manifestChecksum, canonicalChecksum) {
		return fmt.Errorf("manifest checksum verification failed for market %s: recorded %s != canonical order state %s (total_orders=%d, buy_fills=%s, sell_fills=%s)",
			marketID, manifestChecksum, canonicalChecksum, totalOrdersCount, buyFills, sellFills)
	}

	return nil
}

// PostgresSettlementVerifier queries the settlement service table
// to assert that no pending or failed trades remain and that settlement completeness invariants hold.
type PostgresSettlementVerifier struct {
	db pgxConn
}

// NewPostgresSettlementVerifier constructs a new PostgresSettlementVerifier.
func NewPostgresSettlementVerifier(db pgxConn) *PostgresSettlementVerifier {
	return &PostgresSettlementVerifier{db: db}
}

// VerifySettlementInvariants queries the settled_trades table. Fails closed on any error,
// if any pending/failed settlements exist, or if non-SETTLED rows remain.
func (v *PostgresSettlementVerifier) VerifySettlementInvariants(ctx context.Context, marketID string) error {
	if v == nil || v.db == nil {
		return errors.New("settlement verifier database connection is nil")
	}
	if marketID == "" {
		return errors.New("settlement verifier: market_id is required")
	}

	// 1. Check for pending or failed settlements
	var pendingCount int64
	const checkSettlementsSQL = `SELECT COUNT(*) FROM settled_trades WHERE market_id = $1 AND status IN ('PENDING', 'FAILED')`
	err := v.db.QueryRow(ctx, checkSettlementsSQL, marketID).Scan(&pendingCount)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query pending settlements for market %s: %w", marketID, err)
		}
	} else if pendingCount > 0 {
		return fmt.Errorf("unsettled trades detected: settlement service has %d pending/failed trades for market %s",
			pendingCount, marketID)
	}

	// 2. Settlement completeness: all recorded trades must be SETTLED
	var nonSettledCount int64
	const checkNonSettledSQL = `SELECT COUNT(*) FROM settled_trades WHERE market_id = $1 AND status != 'SETTLED'`
	err = v.db.QueryRow(ctx, checkNonSettledSQL, marketID).Scan(&nonSettledCount)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query non-settled status for market %s: %w", marketID, err)
		}
	} else if nonSettledCount > 0 {
		return fmt.Errorf("settlement completeness violation: %d trades in settled_trades are not SETTLED for market %s",
			nonSettledCount, marketID)
	}

	// 3. Trade quantity integrity: all trades must have positive quantity
	var nonPositiveQtyCount int64
	const checkQtySQL = `SELECT COUNT(*) FROM settled_trades WHERE market_id = $1 AND quantity <= 0`
	err = v.db.QueryRow(ctx, checkQtySQL, marketID).Scan(&nonPositiveQtyCount)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query trade quantities for market %s: %w", marketID, err)
		}
	} else if nonPositiveQtyCount > 0 {
		return fmt.Errorf("settlement quantity violation: %d trades have non-positive quantity for market %s",
			nonPositiveQtyCount, marketID)
	}

	return nil
}

// Lease represents a lease object that reports whether it is currently valid.
type Lease interface {
	IsValid() bool
}

// MapLeaseChecker implements LeaseChecker by checking a map of leases.
type MapLeaseChecker struct {
	leases map[int]Lease
}

// NewMapLeaseChecker constructs a new MapLeaseChecker.
func NewMapLeaseChecker(leases map[int]Lease) *MapLeaseChecker {
	return &MapLeaseChecker{leases: leases}
}

// HasLease returns true only if the partition exists in the map and IsValid() is true.
func (m *MapLeaseChecker) HasLease(partition int) bool {
	if m == nil || m.leases == nil {
		return false
	}
	l, ok := m.leases[partition]
	if !ok || l == nil {
		return false
	}
	return l.IsValid()
}
