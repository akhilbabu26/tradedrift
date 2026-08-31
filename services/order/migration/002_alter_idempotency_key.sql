-- +goose Up
ALTER TABLE orders ALTER COLUMN idempotency_key TYPE VARCHAR(64);

-- +goose Down
ALTER TABLE orders ALTER COLUMN idempotency_key TYPE UUID USING idempotency_key::uuid;
