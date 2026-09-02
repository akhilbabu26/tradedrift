package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"tradedrift/services/trade/internal/repository"
)

const (
	defaultUserLimit   = 20
	maxUserLimit       = 100
	defaultMarketLimit = 50
	maxMarketLimit     = 200
)

// Service provides the query-side business logic for Trade Service.
// It handles cursor encoding/decoding, limit clamping, and party-membership
// enforcement for authenticated endpoints.
type Service struct {
	repo repository.Repository
	log  *zap.Logger
}

// NewService creates a new Service.
func NewService(repo repository.Repository, log *zap.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// GetTrade returns a single trade by ID.
// If the trade does not exist, returns repository.ErrTradeNotFound.
// If the caller is not an admin and is neither the buyer nor the seller,
// returns ErrNotParty (mapped by handler to PERMISSION_DENIED).
func (s *Service) GetTrade(ctx context.Context, tradeID uuid.UUID, callerUserID uuid.UUID, isAdmin bool) (*repository.Trade, error) {
	t, err := s.repo.GetByID(ctx, tradeID)
	if err != nil {
		return nil, err // Returns repository.ErrTradeNotFound or DB error
	}
	// TI-8: only buyer, seller, or admin may view a trade.
	if !isAdmin && callerUserID != t.BuyerID && callerUserID != t.SellerID {
		return nil, ErrNotParty
	}
	return t, nil
}

// ListUserTrades returns the authenticated user's fill history, newest-first.
// Cursor-paginated on (executed_at DESC, id DESC).
func (s *Service) ListUserTrades(
	ctx context.Context,
	userID uuid.UUID,
	marketID string,
	cursorStr string,
	limit int32,
) (trades []repository.Trade, nextCursor string, err error) {
	lim := clamp(int(limit), defaultUserLimit, maxUserLimit)

	after, err := decodeCursor(cursorStr)
	if err != nil {
		return nil, "", fmt.Errorf("invalid cursor: %w", err)
	}

	trades, err = s.repo.ListByUser(ctx, userID, marketID, after, lim)
	if err != nil {
		return nil, "", fmt.Errorf("list user trades: %w", err)
	}

	// If we got a full page there may be more — encode the last row as the next cursor.
	if len(trades) == lim {
		last := trades[len(trades)-1]
		nextCursor = encodeCursor(repository.Cursor{
			ExecutedAt: last.ExecutedAt,
			ID:         last.ID,
		})
	}
	return trades, nextCursor, nil
}

// ListMarketTrades returns the public market trade tape, newest-first.
// No user identity enforcement — anonymous callers are permitted.
// The handler is responsible for stripping buyer_id/seller_id from the response.
func (s *Service) ListMarketTrades(
	ctx context.Context,
	marketID string,
	cursorStr string,
	limit int32,
) (trades []repository.Trade, nextCursor string, err error) {
	lim := clamp(int(limit), defaultMarketLimit, maxMarketLimit)

	after, err := decodeCursor(cursorStr)
	if err != nil {
		return nil, "", fmt.Errorf("invalid cursor: %w", err)
	}

	trades, err = s.repo.ListByMarket(ctx, marketID, after, lim)
	if err != nil {
		return nil, "", fmt.Errorf("list market trades: %w", err)
	}

	if len(trades) == lim {
		last := trades[len(trades)-1]
		nextCursor = encodeCursor(repository.Cursor{
			ExecutedAt: last.ExecutedAt,
			ID:         last.ID,
		})
	}
	return trades, nextCursor, nil
}

// ── Cursor encode / decode ────────────────────────────────────────────────────

// encodeCursor serialises a keyset cursor as a URL-safe base64 string.
// Format: base64(unix_nano_string + ":" + uuid_string)
// Opaque to callers — never parse the format outside this package.
func encodeCursor(c repository.Cursor) string {
	raw := fmt.Sprintf("%d:%s", c.ExecutedAt.UnixNano(), c.ID.String())
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses a cursor produced by encodeCursor.
// Returns nil, nil when cursorStr is empty (first page — no cursor).
func decodeCursor(cursorStr string) (*repository.Cursor, error) {
	if cursorStr == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursorStr)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed cursor")
	}
	var nanos int64
	if _, err := fmt.Sscanf(parts[0], "%d", &nanos); err != nil {
		return nil, fmt.Errorf("cursor timestamp: %w", err)
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return nil, fmt.Errorf("cursor uuid: %w", err)
	}
	return &repository.Cursor{
		ExecutedAt: time.Unix(0, nanos).UTC(),
		ID:         id,
	}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// clamp returns limit clamped to [defaultLim, maxLim].
// If limit <= 0 the default is used.
func clamp(limit, defaultLim, maxLim int) int {
	if limit <= 0 {
		return defaultLim
	}
	if limit > maxLim {
		return maxLim
	}
	return limit
}

// ErrNotParty is returned when the caller is neither buyer nor seller of a trade.
// The gRPC handler maps this to codes.PermissionDenied.
var ErrNotParty = fmt.Errorf("caller is not a party to this trade")
