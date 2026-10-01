-- +goose Up

-- River job queue tables (in payments schema)
-- These are the core tables River needs to function.

CREATE TABLE payments.river_job (
    id BIGSERIAL PRIMARY KEY,
    state TEXT NOT NULL DEFAULT 'available',
    attempt INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL,
    attempted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at TIMESTAMPTZ,
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    priority INTEGER NOT NULL DEFAULT 1,
    args JSONB NOT NULL,
    attempted_by TEXT[],
    errors JSONB[],
    kind TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    queue TEXT NOT NULL DEFAULT 'default',
    tags TEXT[] NOT NULL DEFAULT '{}'
);

CREATE INDEX river_job_state_idx ON payments.river_job (state);
CREATE INDEX river_job_kind_idx ON payments.river_job (kind);
CREATE INDEX river_job_scheduled_at_idx ON payments.river_job (scheduled_at);
CREATE INDEX river_job_queue_idx ON payments.river_job (queue);

CREATE TABLE payments.river_leader (
    elected_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    leader_id TEXT NOT NULL,
    name TEXT PRIMARY KEY
);

-- +goose Down
DROP TABLE IF EXISTS payments.river_leader;
DROP TABLE IF EXISTS payments.river_job;
