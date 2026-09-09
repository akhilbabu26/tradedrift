-- +goose Up
-- Partial index for rapid outbox retention cleanup without impacting active outbox polling
CREATE INDEX IF NOT EXISTS idx_notification_outbox_processed 
    ON notification_outbox(published_at, target_channel) 
    WHERE status = 'PROCESSED';

-- +goose Down
DROP INDEX IF EXISTS idx_notification_outbox_processed;
