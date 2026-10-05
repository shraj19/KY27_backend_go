-- +goose Up
ALTER TABLE payments.orders ADD COLUMN IF NOT EXISTS paid_at timestamptz;

-- +goose Down
ALTER TABLE payments.orders DROP COLUMN IF EXISTS paid_at;
