-- name: CreateOrder :one
INSERT INTO payments.orders (
    id, buyer_id, total_paise, currency, status, provider,
    provider_order_id, expires_at, buyer_phone, buyer_email, buyer_name
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: GetOrder :one
SELECT * FROM payments.orders WHERE id = $1;

-- name: GetOrderByProviderOrderID :one
SELECT * FROM payments.orders WHERE provider_order_id = $1;

-- name: GetOrderStatus :one
SELECT id, status, total_paise FROM payments.orders WHERE id = $1;

-- name: MarkOrderPaidIfActive :one
-- Idempotent: only transitions ACTIVE → PAID when amount matches.
-- Returns the row if transition happened, no rows otherwise.
UPDATE payments.orders
SET status = 'PAID', 
    paid_at = $2,
    updated_at = now()
WHERE id = $1 
  AND status = 'ACTIVE' 
  AND total_paise = $3
RETURNING id;

-- name: UpdateOrderStatus :one
UPDATE payments.orders
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: FindIdempotencyKey :one
SELECT * FROM payments.idempotency_keys WHERE key = $1;

-- name: InsertIdempotencyKey :one
INSERT INTO payments.idempotency_keys (key, body_hash, order_id)
VALUES ($1, $2, $3)
RETURNING *;
