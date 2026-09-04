package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"tradedrift/services/wallet/internal/repository"
)

type ReservationRepository struct {
	db repository.DBTX
}

func NewReservationRepository(db repository.DBTX) *ReservationRepository {
	return &ReservationRepository{db: db}
}

func (r *ReservationRepository) WithTx(tx pgx.Tx) repository.ReservationRepository {
	return &ReservationRepository{db: tx}
}

func (r *ReservationRepository) Create(ctx context.Context, res *repository.Reservation) error {
	query := `
		INSERT INTO wallet_reservations (
			id, order_id, user_id, asset,
			reserved_amount, consumed_amount, remaining_amount,
			status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := r.db.Exec(ctx, query,
		res.ID, res.OrderID, res.UserID, res.Asset,
		res.ReservedAmount, res.ConsumedAmount, res.RemainingAmount,
		res.Status, res.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert reservation: %w", err)
	}
	return nil
}

func (r *ReservationRepository) GetByOrderID(ctx context.Context, orderID string) (*repository.Reservation, error) {
	query := `
		SELECT id, order_id, user_id, asset,
		       reserved_amount, consumed_amount, remaining_amount,
		       status, created_at
		FROM wallet_reservations
		WHERE order_id = $1
	`
	var res repository.Reservation
	err := r.db.QueryRow(ctx, query, orderID).Scan(
		&res.ID, &res.OrderID, &res.UserID, &res.Asset,
		&res.ReservedAmount, &res.ConsumedAmount, &res.RemainingAmount,
		&res.Status, &res.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query reservation by order_id: %w", err)
	}
	return &res, nil
}

// GetByOrderIDForUpdate locks the reservation row using SELECT ... FOR UPDATE.
// Serializes concurrent partial fills against the same order.
func (r *ReservationRepository) GetByOrderIDForUpdate(ctx context.Context, orderID string) (*repository.Reservation, error) {
	query := `
		SELECT id, order_id, user_id, asset,
		       reserved_amount, consumed_amount, remaining_amount,
		       status, created_at
		FROM wallet_reservations
		WHERE order_id = $1
		FOR UPDATE
	`
	var res repository.Reservation
	err := r.db.QueryRow(ctx, query, orderID).Scan(
		&res.ID, &res.OrderID, &res.UserID, &res.Asset,
		&res.ReservedAmount, &res.ConsumedAmount, &res.RemainingAmount,
		&res.Status, &res.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query reservation by order_id for update: %w", err)
	}
	return &res, nil
}

func (r *ReservationRepository) UpdateStatus(ctx context.Context, reservationID, status string) error {
	query := `
		UPDATE wallet_reservations
		SET status = $1
		WHERE id = $2
	`
	res, err := r.db.Exec(ctx, query, status, reservationID)
	if err != nil {
		return fmt.Errorf("failed to update reservation status: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("reservation not found: %s", reservationID)
	}
	return nil
}

func (r *ReservationRepository) UpdateConsumed(ctx context.Context, reservationID, consumedAmount, remainingAmount string) error {
	query := `
		UPDATE wallet_reservations
		SET consumed_amount  = $1::DECIMAL,
		    remaining_amount = $2::DECIMAL,
		    status = CASE
		        WHEN $2::DECIMAL = 0 THEN 'CONSUMED'
		        ELSE 'PARTIALLY_CONSUMED'
		    END
		WHERE id = $3
	`
	res, err := r.db.Exec(ctx, query, consumedAmount, remainingAmount, reservationID)
	if err != nil {
		return fmt.Errorf("failed to update consumed amount: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("reservation not found: %s", reservationID)
	}
	return nil
}

// ConsumeRemaining atomically increments consumed_amount and decrements remaining_amount in SQL.
// Ensures remaining_amount >= amount via WHERE condition.
// Returns ErrInsufficientReservation if RowsAffected == 0.
func (r *ReservationRepository) ConsumeRemaining(ctx context.Context, reservationID, amount string) error {
	query := `
		UPDATE wallet_reservations
		SET consumed_amount  = consumed_amount + $1::DECIMAL,
		    remaining_amount = remaining_amount - $1::DECIMAL,
		    status = CASE
		        WHEN remaining_amount - $1::DECIMAL <= 0 THEN 'CONSUMED'
		        ELSE 'PARTIALLY_CONSUMED'
		    END
		WHERE id = $2
		  AND remaining_amount >= $1::DECIMAL
	`
	res, err := r.db.Exec(ctx, query, amount, reservationID)
	if err != nil {
		return fmt.Errorf("failed to consume reservation remaining: %w", err)
	}
	if res.RowsAffected() == 0 {
		return repository.ErrInsufficientReservation
	}
	return nil
}

// Compile-time check.
var _ repository.ReservationRepository = (*ReservationRepository)(nil)
