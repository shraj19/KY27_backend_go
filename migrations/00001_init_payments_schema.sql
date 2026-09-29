-- +goose Up
-- +goose StatementBegin

-- Schema owned exclusively by the Go payment service (schema-per-service).
CREATE SCHEMA IF NOT EXISTS payments;

-- Order status. Mirrors payment.Status in Go. Kept as text + CHECK rather than a
-- pg enum so we can add values without an ALTER TYPE migration dance.
-- ACTIVE = created/unpaid, PAID, EXPIRED, FAILED.

CREATE TABLE payments.orders (
    id               text        PRIMARY KEY,             -- our order id (KY27-<uuid>)
    user_id          text        NOT NULL,                -- trusted value from JWT (NOT an FK to auth.users)
    pass_id          text        NOT NULL,                -- catalog id (owned by Node)
    coupon_id        text,                                -- optional
    amount_paise     bigint      NOT NULL CHECK (amount_paise >= 0),
    currency         text        NOT NULL DEFAULT 'INR',
    status           text        NOT NULL DEFAULT 'ACTIVE'
                       CHECK (status IN ('ACTIVE','PAID','EXPIRED','FAILED')),
    provider         text        NOT NULL,                -- cashfree | konfhub | ...
    provider_order_id text,                               -- gateway's own id
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL
);
CREATE INDEX orders_user_id_idx ON payments.orders (user_id);

-- One pass per user: the LOAD-BEARING correctness guarantee. Only PAID rows must
-- be unique per user, so failed/expired retries are allowed. Partial unique index.
CREATE UNIQUE INDEX orders_one_paid_pass_per_user
    ON payments.orders (user_id)
    WHERE status = 'PAID';

-- Idempotency: dedupe identical retries. The key comes from the frontend; the
-- body_hash binds the key to the exact request (same key + different body => reject).
CREATE TABLE payments.idempotency_keys (
    key         text        PRIMARY KEY,
    body_hash   text        NOT NULL,
    order_id    text        NOT NULL REFERENCES payments.orders(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Short-lived per-user lock (UX / fail-fast). Expiry >= gateway order expiry.
-- Not the correctness guarantee (that's the partial unique index above).
CREATE TABLE payments.payment_locks (
    user_id     text        PRIMARY KEY,
    order_id    text        NOT NULL,
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Coupon redemptions: enforce per-coupon and per-user usage limits locally.
CREATE TABLE payments.coupon_redemptions (
    id          text        PRIMARY KEY,
    coupon_id   text        NOT NULL,
    user_id     text        NOT NULL,
    order_id    text        NOT NULL REFERENCES payments.orders(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (coupon_id, user_id)   -- a user redeems a given coupon at most once
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS payments.coupon_redemptions;
DROP TABLE IF EXISTS payments.payment_locks;
DROP TABLE IF EXISTS payments.idempotency_keys;
DROP TABLE IF EXISTS payments.orders;
DROP SCHEMA IF EXISTS payments;
-- +goose StatementEnd
