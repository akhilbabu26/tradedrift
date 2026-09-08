-- +goose Up
-- SQL in section 'Up' is executed when this migration is applied

ALTER TABLE processed_user_trades ADD COLUMN IF NOT EXISTS order_id UUID;
ALTER TABLE processed_user_trades ADD COLUMN IF NOT EXISTS role VARCHAR(10);
ALTER TABLE processed_user_trades ADD COLUMN IF NOT EXISTS price DECIMAL(30,10);
ALTER TABLE processed_user_trades ADD COLUMN IF NOT EXISTS quantity DECIMAL(30,10);

-- Backfill any pre-existing test rows before applying NOT NULL constraints
UPDATE processed_user_trades SET order_id = '00000000-0000-0000-0000-000000000000' WHERE order_id IS NULL;
UPDATE processed_user_trades SET role = 'BUY' WHERE role IS NULL;
UPDATE processed_user_trades SET price = 0 WHERE price IS NULL;
UPDATE processed_user_trades SET quantity = 0 WHERE quantity IS NULL;

ALTER TABLE processed_user_trades ALTER COLUMN order_id SET NOT NULL;
ALTER TABLE processed_user_trades ALTER COLUMN role SET NOT NULL;
ALTER TABLE processed_user_trades ALTER COLUMN price SET NOT NULL;
ALTER TABLE processed_user_trades ALTER COLUMN quantity SET NOT NULL;

-- +goose Down
-- SQL in section 'Down' is executed when this migration is rolled back

ALTER TABLE processed_user_trades DROP COLUMN IF EXISTS quantity;
ALTER TABLE processed_user_trades DROP COLUMN IF EXISTS price;
ALTER TABLE processed_user_trades DROP COLUMN IF EXISTS role;
ALTER TABLE processed_user_trades DROP COLUMN IF EXISTS order_id;
