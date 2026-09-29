-- name: CreateOrder :one
INSERT INTO payments.orders (
    id, user_id, pass_id, coupon_id, amount_paise, currency,
    status, provider, provider_order_id, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

-- name: GetOrder :one
SELECT * FROM payments.orders WHERE id = $1;

-- name: MarkOrderPaid :one
UPDATE payments.orders
SET status = 'PAID', provider_order_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UserHasPaidPass :one
SELECT EXISTS (
    SELECT 1 FROM payments.orders
    WHERE user_id = $1 AND status = 'PAID'
) AS has_pass;

-- name: FindIdempotencyKey :one
SELECT * FROM payments.idempotency_keys WHERE key = $1;

-- name: InsertIdempotencyKey :one
INSERT INTO payments.idempotency_keys (key, body_hash, order_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpsertPaymentLock :one
INSERT INTO payments.payment_locks (user_id, order_id, expires_at)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE
    SET order_id = EXCLUDED.order_id, expires_at = EXCLUDED.expires_at
    WHERE payments.payment_locks.expires_at < now()   -- only steal an expired lock
RETURNING *;

-- name: DeletePaymentLock :exec
DELETE FROM payments.payment_locks WHERE user_id = $1;

-- name: InsertCouponRedemption :one
INSERT INTO payments.coupon_redemptions (id, coupon_id, user_id, order_id)
VALUES ($1, $2, $3, $4)
RETURNING *;
