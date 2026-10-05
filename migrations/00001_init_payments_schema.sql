-- +goose Up
-- +goose StatementBegin

-- Schema owned exclusively by the Go payment service (schema-per-service).
-- Node owns cart/catalog/fulfillment; this service just collects money.
CREATE SCHEMA IF NOT EXISTS payments;

-- Orders: the payment transaction. One buyer, one total amount.
-- Line items (tickets/attendees) stay in Node - payment service only cares about totals.
CREATE TABLE payments.orders (
    id               text        PRIMARY KEY,
    buyer_id         text        NOT NULL,
    total_paise      bigint      NOT NULL CHECK (total_paise >= 0),
    currency         text        NOT NULL DEFAULT 'INR',
    status           text        NOT NULL DEFAULT 'ACTIVE'
                       CHECK (status IN ('ACTIVE','PAID','EXPIRED','FAILED')),
    provider         text        NOT NULL,
    provider_order_id text,
    buyer_phone      text        NOT NULL,
    buyer_email      text        NOT NULL,
    buyer_name       text        NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL
);
CREATE INDEX orders_buyer_id_idx ON payments.orders (buyer_id);

-- Idempotency: dedupe identical requests. Key from caller, body_hash binds it
-- to the exact payload (same key + different body = reject).
CREATE TABLE payments.idempotency_keys (
    key         text        PRIMARY KEY,
    body_hash   text        NOT NULL,
    order_id    text        NOT NULL REFERENCES payments.orders(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS payments.idempotency_keys;
DROP TABLE IF EXISTS payments.orders;
DROP SCHEMA IF EXISTS payments;
-- +goose StatementEnd
