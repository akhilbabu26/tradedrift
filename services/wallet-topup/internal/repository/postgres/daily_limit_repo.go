package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/repository"
)

type DailyLimitRepo struct {
	db *pgxpool.Pool
}

var _ repository.DailyLimitRepository = (*DailyLimitRepo)(nil)

func NewDailyLimitRepo(db *pgxpool.Pool) *DailyLimitRepo {

	return &DailyLimitRepo{db: db}
}

// EnsureDailyLimit guarantees a daily row exists for the user and usage date (default limit: 10 INR).
func (r *DailyLimitRepo) EnsureDailyLimit(ctx context.Context, userID, usageDate string, limitINR int64) error {
	query := `
		INSERT INTO daily_topup_limits (user_id, usage_date, limit_inr, reserved_inr, consumed_inr)
		VALUES ($1, $2, $3, 0, 0)
		ON CONFLICT (user_id, usage_date) DO NOTHING;
	`
	_, err := r.db.Exec(ctx, query, userID, usageDate, limitINR)
	if err != nil {
		return fmt.Errorf("failed to ensure daily limit record: %w", err)
	}
	return nil
}

// ReserveQuota atomically increments reserved_inr if remaining capacity allows.
// Returns (true, nil) if reserved successfully, (false, nil) if quota exceeded.
func (r *DailyLimitRepo) ReserveQuota(ctx context.Context, userID, usageDate string, amountINR, limitINR int64) (bool, error) {
	if err := r.EnsureDailyLimit(ctx, userID, usageDate, limitINR); err != nil {
		return false, err
	}


	query := `
		UPDATE daily_topup_limits
		SET reserved_inr = reserved_inr + $1,
		    updated_at   = NOW()
		WHERE user_id    = $2
		  AND usage_date = $3
		  AND (reserved_inr + consumed_inr + $1) <= limit_inr;
	`
	tag, err := r.db.Exec(ctx, query, amountINR, userID, usageDate)
	if err != nil {
		return false, fmt.Errorf("failed to execute quota reservation: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseReservedQuota decrements reserved_inr strictly with invariant check (reserved_inr >= amount).
func (r *DailyLimitRepo) ReleaseReservedQuota(ctx context.Context, userID, usageDate string, amountINR int64) error {
	query := `
		UPDATE daily_topup_limits
		SET reserved_inr = reserved_inr - $1,
		    updated_at   = NOW()
		WHERE user_id    = $2
		  AND usage_date = $3
		  AND reserved_inr >= $1;
	`
	tag, err := r.db.Exec(ctx, query, amountINR, userID, usageDate)
	if err != nil {
		return fmt.Errorf("failed to release reserved quota: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("invariant violation: cannot release %d INR reserved quota for user %s on %s", amountINR, userID, usageDate)
	}
	return nil
}

// ConsumeReservedQuota atomically shifts amount from reserved_inr to consumed_inr on same-day payment.
func (r *DailyLimitRepo) ConsumeReservedQuota(ctx context.Context, userID, usageDate string, amountINR int64) error {
	query := `
		UPDATE daily_topup_limits
		SET reserved_inr = reserved_inr - $1,
		    consumed_inr = consumed_inr + $1,
		    updated_at   = NOW()
		WHERE user_id    = $2
		  AND usage_date = $3
		  AND reserved_inr >= $1;
	`
	tag, err := r.db.Exec(ctx, query, amountINR, userID, usageDate)
	if err != nil {
		return fmt.Errorf("failed to consume reserved quota: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("invariant violation: cannot consume %d INR reserved quota for user %s on %s", amountINR, userID, usageDate)
	}
	return nil
}

// AttemptDirectConsumption attempts to consume quota directly on Day 2 without prior Day 2 reservation (cross-midnight).
// Returns (true, nil) if Day 2 quota available and consumed, (false, nil) if Day 2 quota exhausted.
func (r *DailyLimitRepo) AttemptDirectConsumption(ctx context.Context, userID, usageDate string, amountINR, limitINR int64) (bool, error) {
	if err := r.EnsureDailyLimit(ctx, userID, usageDate, limitINR); err != nil {
		return false, err
	}

	query := `
		UPDATE daily_topup_limits
		SET consumed_inr = consumed_inr + $1,
		    updated_at   = NOW()
		WHERE user_id    = $2
		  AND usage_date = $3
		  AND (reserved_inr + consumed_inr + $1) <= limit_inr;
	`
	tag, err := r.db.Exec(ctx, query, amountINR, userID, usageDate)
	if err != nil {
		return false, fmt.Errorf("failed to attempt direct quota consumption: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// GetDailyUsage fetches the current usage record for a user on a given date.
func (r *DailyLimitRepo) GetDailyUsage(ctx context.Context, userID, usageDate string, limitINR int64) (*domain.DailyTopUpLimit, error) {
	if err := r.EnsureDailyLimit(ctx, userID, usageDate, limitINR); err != nil {
		return nil, err
	}

	query := `
		SELECT user_id, usage_date, limit_inr, reserved_inr, consumed_inr, created_at, updated_at
		FROM daily_topup_limits
		WHERE user_id = $1 AND usage_date = $2;
	`
	row := r.db.QueryRow(ctx, query, userID, usageDate)
	var limit domain.DailyTopUpLimit
	var uDate time.Time
	err := row.Scan(
		&limit.UserID,
		&uDate,
		&limit.LimitINR,
		&limit.ReservedINR,
		&limit.ConsumedINR,
		&limit.CreatedAt,
		&limit.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &domain.DailyTopUpLimit{
				UserID:    userID,
				UsageDate: usageDate,
				LimitINR:  limitINR,
			}, nil
		}
		return nil, fmt.Errorf("failed to fetch daily topup limit: %w", err)
	}
	limit.UsageDate = uDate.Format("2006-01-02")
	return &limit, nil
}

