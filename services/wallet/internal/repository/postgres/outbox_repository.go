package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/wallet/internal/repository"
)

type OutboxRepository struct {
	db *pgxpool.Pool
}

func NewOutboxRepository(db *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{db: db}
}

func (r *OutboxRepository) Insert(ctx context.Context, event *repository.OutboxEvent) error {
	query := `
		INSERT INTO outbox (
			id, aggregate_id, event_type, payload,
			partition_key, status, created_at
		) VALUES ($1, $2, $3, $4, $5, 'PENDING', $6)
	`
	_, err := r.db.Exec(ctx, query,
		event.ID, event.AggregateID, event.EventType, event.Payload,
		event.PartitionKey, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("failed to insert outbox event: %w", err)
	}
	return nil
}
