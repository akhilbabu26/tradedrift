package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reservation represents a fund lock for a specific order.
type Reservation struct{
	ID              string
	OrderID         string
	UserID          string
	Asset           string
	ReservedAmount  string
	ConsumedAmount  string
	RemainingAmount string
	Status          string // ACTIVE | PARTIALLY_CONSUMED | CONSUMED | RELEASED
	CreatedAt       time.Time
}

// ReservationRepository defines the persistence contract for order fund locks.
type ReservationRepository interface {
	// WithTx binds the repository to an active PostgreSQL transaction.
	WithTx(tx pgx.Tx) ReservationRepository
	// Create inserts a new reservation row.
	Create(ctx context.Context, r *Reservation) error
	// GetByOrderID retrieves the reservation for an order.
	// Returns nil, nil if not found.
	GetByOrderID(ctx context.Context, orderID string) (*Reservation, error)
	// GetByOrderIDForUpdate retrieves and locks the reservation row for an order using FOR UPDATE.
	// Returns nil, nil if not found.
	GetByOrderIDForUpdate(ctx context.Context, orderID string) (*Reservation, error)
	// UpdateStatus updates the reservation status.
	UpdateStatus(ctx context.Context, reservationID, status string) error
	// UpdateConsumed updates consumed_amount and remaining_amount after a partial fill.
	UpdateConsumed(ctx context.Context, reservationID, consumedAmount, remainingAmount string) error
	// ConsumeRemaining atomically decrements remaining_amount and increments consumed_amount in SQL,
	// returning ErrInsufficientReservation if remaining_amount < amount.
	ConsumeRemaining(ctx context.Context, reservationID, amount string) error
}