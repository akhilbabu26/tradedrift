package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/repository"
)

type WebhookEventRepo struct {
	db *pgxpool.Pool
}

var _ repository.WebhookEventRepository = (*WebhookEventRepo)(nil)

func NewWebhookEventRepo(db *pgxpool.Pool) *WebhookEventRepo {

	return &WebhookEventRepo{db: db}
}

// RecordVerifiedEvent writes a cryptographically verified webhook event.
// Returns alreadyProcessed = true if the event was already recorded (deduplication).
func (r *WebhookEventRepo) RecordVerifiedEvent(ctx context.Context, event *domain.WebhookEvent) (bool, error) {
	query := `
		INSERT INTO webhook_events (
			id, provider, event_id, payment_id, signature_valid, payload,
			status, error_message, received_at, processed_at
		) VALUES (
			$1, $2, $3, $4, TRUE, $5,
			$6, $7, $8, $9
		);
	`
	_, err := r.db.Exec(ctx, query,
		event.ID, event.Provider, event.EventID, event.PaymentID, event.Payload,
		event.Status, event.ErrorMessage, event.ReceivedAt, event.ProcessedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Deduplicated against idx_webhook_verified_dedup
			return true, nil
		}
		return false, fmt.Errorf("failed to insert verified webhook event: %w", err)
	}
	return false, nil
}

// RecordFailedSignatureEvent records an event with invalid signature for security audit.
// Does NOT trigger verified deduplication.
func (r *WebhookEventRepo) RecordFailedSignatureEvent(ctx context.Context, event *domain.WebhookEvent) error {
	query := `
		INSERT INTO webhook_events (
			id, provider, event_id, payment_id, signature_valid, payload,
			status, error_message, received_at, processed_at
		) VALUES (
			$1, $2, $3, $4, FALSE, $5,
			'FAILED', $6, $7, NOW()
		);
	`
	_, err := r.db.Exec(ctx, query,
		event.ID, event.Provider, event.EventID, event.PaymentID, event.Payload,
		event.ErrorMessage, event.ReceivedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert failed-signature webhook audit: %w", err)
	}
	return nil
}

// RecordEvent records any arbitrary webhook audit row with explicit signature_valid and status values.
func (r *WebhookEventRepo) RecordEvent(ctx context.Context, event *domain.WebhookEvent) error {
	query := `
		INSERT INTO webhook_events (
			id, provider, event_id, payment_id, signature_valid, payload,
			status, error_message, received_at, processed_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10
		);
	`
	_, err := r.db.Exec(ctx, query,
		event.ID, event.Provider, event.EventID, event.PaymentID, event.SignatureValid, event.Payload,
		event.Status, event.ErrorMessage, event.ReceivedAt, event.ProcessedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to record webhook audit event: %w", err)
	}
	return nil
}

// UpdateEventStatus updates status, error_message, and sets processed_at = NOW().
func (r *WebhookEventRepo) UpdateEventStatus(ctx context.Context, eventID, status string, errMsg *string) error {
	query := `
		UPDATE webhook_events
		SET status        = $1,
		    error_message = $2,
		    processed_at  = NOW()
		WHERE id = $3;
	`
	_, err := r.db.Exec(ctx, query, status, errMsg, eventID)
	if err != nil {
		return fmt.Errorf("failed to update webhook event status: %w", err)
	}
	return nil
}
