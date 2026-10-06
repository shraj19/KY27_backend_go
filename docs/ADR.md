# Design Decisions

This document captures key design decisions, alternatives considered, and rationale.

---

# Part 1: Tech Stack

## TS-001: Go for Payment Service

**Context:** Need a backend service for payment processing alongside existing Node.js backend.

**Options:**
1. **Node.js (TypeScript)** — same stack as main backend, team familiarity
2. **Go** — strong typing, good concurrency, fast cold starts

**Decision:** Go

**Rationale:**
- Learning project goal: understand Go patterns deeply
- Payment processing benefits from Go's type safety and explicit error handling
- Good fit for gRPC services
- Forces clear service boundary (can't share code with Node)

---

## TS-002: Gin for HTTP Framework

**Context:** Need HTTP server for webhooks and health checks.

**Options:**
1. **net/http (stdlib)** — no dependencies, verbose
2. **Gin** — fast, middleware support, popular
3. **Chi** — lightweight, stdlib-compatible
4. **Fiber** — fastest, Express-like API

**Decision:** Gin

**Rationale:**
- Good balance of features and simplicity
- Built-in JSON binding/validation
- Large ecosystem of middleware
- Well-documented

---

## TS-003: sqlc for Database Access

**Context:** Need to interact with PostgreSQL.

**Options:**
1. **database/sql + raw queries** — verbose, no type safety
2. **GORM** — ORM, magic, implicit behavior
3. **sqlx** — sql + struct scanning
4. **sqlc** — generates Go from SQL, type-safe

**Decision:** sqlc

**Rationale:**
- Write real SQL (no ORM magic)
- Compile-time type safety
- Generated code is readable and debuggable
- `emit_interface: true` enables easy mocking for tests
- SQL stays in `.sql` files — easy to review

---

## TS-004: PostgreSQL (Neon) for Database

**Context:** Need a relational database.

**Options:**
1. **PostgreSQL (self-hosted)** — full control, ops burden
2. **Neon** — serverless Postgres, free tier, branching
3. **PlanetScale** — serverless MySQL
4. **Supabase** — Postgres + extras

**Decision:** Neon PostgreSQL

**Rationale:**
- Serverless with generous free tier
- Connection pooling built-in (important for serverless)
- Database branching for testing
- Standard Postgres — no lock-in

---

## TS-005: River for Job Queue

**Context:** Need async job processing for Node notifications.

**Options:**
1. **Direct HTTP calls** — simple, no retry on failure
2. **Redis + custom worker** — fast, requires Redis infra
3. **AWS SQS** — managed, adds AWS dependency
4. **River** — Postgres-backed, transactional enqueue

**Decision:** River

**Rationale:**
- Uses existing Postgres — no new infrastructure
- Transactional enqueue: job insert + DB update in same transaction
- Built-in retries with backoff
- Jobs survive server restarts
- Go-native, well-designed API

---

## TS-006: gRPC for Node ↔ Go Communication

**Context:** Node backend needs to call Go payment service.

**Options:**
1. **REST/HTTP** — simple, universal
2. **gRPC** — typed contracts, efficient, streaming
3. **GraphQL** — flexible queries, overkill for service-to-service

**Decision:** gRPC with TLS + token auth

**Rationale:**
- Proto files define contract — both services know exact types
- Efficient binary serialization
- Built-in code generation for both Go and Node
- TLS for encryption, token auth for authorization
- Learning goal: understand gRPC patterns

---

## TS-007: OpenTelemetry for Observability

**Context:** Need traces, metrics, and logs in production.

**Options:**
1. **Custom logging + Prometheus client** — manual, fragmented
2. **Datadog/New Relic SDK** — vendor lock-in
3. **OpenTelemetry** — vendor-neutral, unified API

**Decision:** OpenTelemetry SDK → Grafana Cloud

**Rationale:**
- Single SDK for traces, metrics, logs
- Vendor-neutral — can switch backends
- Auto-instrumentation for gRPC
- `trace_id` correlation across all signals
- Grafana Cloud has free tier

---

## TS-008: AWS SSM Parameter Store for Production Secrets

**Context:** Need secure config management for production.

**Options:**
1. **.env file on server** — simple, not secure for prod
2. **AWS Secrets Manager** — auto-rotation, $0.40/secret/month
3. **AWS SSM Parameter Store** — free, encrypted, IAM-controlled
4. **HashiCorp Vault** — powerful, complex to operate

**Decision:** SSM Parameter Store (prod), .env file (dev/test)

**Rationale:**
- Free for all parameter types
- KMS encryption for SecureString
- IAM access control + CloudTrail audit
- No auto-rotation needed (Cashfree keys don't rotate automatically)
- Simple: fetch all params under a path prefix

---

# Part 2: Architecture Decisions

## ADR-001: Node Owns Line Items, Go Owns Payments

**Status:** Accepted

**Context:** Payment flow involves cart items, pricing, and payment processing. Where does each responsibility live?

**Options:**
1. **Go stores line items** — duplicate data, sync complexity
2. **Node owns items, Go just collects money** — clear boundary

**Decision:** Go payment service only knows: buyer, total amount, status. Node owns cart, pricing, tickets, fulfillment.

**Rationale:**
- Single source of truth for pricing (Node)
- Go service is simpler — just a payment state machine
- No data sync issues
- Clear service boundary

**Consequences:**
- Go trusts the `total_paise` from Node
- Node must call Go for payment, then fulfill locally on success
- Refunds require coordination between services

---

## ADR-002: Webhook Idempotency via SQL WHERE Clause

**Status:** Accepted

**Context:** Payment gateways send webhooks at-least-once. Duplicates must not cause double-fulfillment.

**Options:**
1. **Check status in code, then update** — race condition window
2. **Idempotency key table** — extra table, extra queries
3. **Conditional UPDATE with RETURNING** — atomic, single query

**Decision:** 
```sql
UPDATE orders SET status = 'PAID', paid_at = $2
WHERE id = $1 AND status = 'ACTIVE' AND total_paise = $3
RETURNING id;
```

**Rationale:**
- Atomic: no race between check and update
- Also verifies amount matches (prevents underpayment)
- Returns row only if transition happened — controls job enqueue
- Single query, no extra tables

**Consequences:**
- Must check `ErrNoRows` to distinguish: already paid, not found, amount mismatch
- Added `GetOrderStatus` query for diagnostics when no transition

---

## ADR-003: Order Lifecycle Consolidated in order.Service

**Status:** Accepted

**Context:** Order states (ACTIVE → PAID/FAILED/EXPIRED) were split across order and webhook packages.

**Options:**
1. **Keep split** — webhook handler owns PAID transition
2. **Consolidate** — order.Service owns all state transitions

**Decision:** All state transitions in `order.Service`:
- `Create()` → ACTIVE
- `Complete()` → PAID + enqueue notification
- `Fail()` → FAILED
- `Expire()` → EXPIRED

**Rationale:**
- Locality: understand state machine in one file
- Webhook handler becomes thin HTTP adapter
- Easier to test state transition logic
- Clear ownership

**Consequences:**
- Webhook handler just parses and calls `orderSvc.Complete()`
- order.Service needs TxBeginner and JobInserter interfaces

---

## ADR-004: Interface Seams for Testability

**Status:** Accepted

**Context:** Services depend on database and job queue. Need unit tests without real infrastructure.

**Options:**
1. **Integration tests only** — slow, needs DB
2. **Custom interfaces per module** — lots of boilerplate
3. **sqlc emit_interface + minimal custom interfaces** — auto-generated + targeted

**Decision:**
- `emit_interface: true` in sqlc → generates `db.Querier`
- `TxBeginner` interface for `*pgxpool.Pool`
- `JobInserter` interface for River client
- `QuerierFactory` for injecting mock in transactional code

**Rationale:**
- sqlc interface stays in sync with queries automatically
- Minimal custom interfaces (only where sqlc doesn't help)
- Tests inject fakes, production injects real implementations

**Consequences:**
- `order.Service` takes interfaces, not concrete types
- `WithQuerierFactory()` allows test injection
- Can unit test `Complete()` without database

---

## ADR-005: River for Transactional Job Enqueue

**Status:** Accepted

**Context:** When marking order PAID, must also notify Node. These must be atomic.

**Options:**
1. **Update DB, then HTTP call** — if HTTP fails, inconsistent state
2. **HTTP call, then update DB** — if DB fails, Node thinks paid but DB doesn't
3. **Transactional outbox (River)** — job enqueued in same transaction as DB update

**Decision:** River with `InsertTx()` in same transaction as `MarkOrderPaidIfActive`.

**Rationale:**
- Atomic: either both happen or neither
- River retries failed jobs automatically
- Jobs survive server restart (persisted in Postgres)
- No need for separate queue infrastructure

**Consequences:**
- Need direct Postgres connection for River (not pooler) due to LISTEN/NOTIFY
- Two connection pools: one for app queries, one for River

---

## ADR-006: gRPC with TLS + Token Auth for Internal Services

**Status:** Accepted

**Context:** Node backend calls Go payment service. Need secure communication.

**Options:**
1. **HTTP + API key header** — simple, works
2. **gRPC + TLS + token** — typed, encrypted, authenticated
3. **mTLS** — both sides verify certificates, complex setup

**Decision:** gRPC with server TLS + bearer token in metadata.

**Rationale:**
- TLS encrypts traffic
- Token auth is simple (shared secret between services)
- gRPC gives typed contracts via protobuf
- mTLS overkill for single-cluster deployment

**Consequences:**
- Need to generate and manage TLS certificates
- Token stored in both services' config
- Auth interceptor validates token on every request

---

## ADR-007: SSM for Production, .env for Development

**Status:** Accepted

**Context:** Need different config sources for different environments.

**Options:**
1. **.env everywhere** — simple, not secure for prod
2. **SSM everywhere** — secure, awkward for local dev
3. **Conditional: SSM for prod, .env for dev** — best of both

**Decision:**
```go
if appEnv == "production" {
    return loadFromSSM()
}
return loadFromEnvFile()
```

**Rationale:**
- Local dev: just edit `.env`, no AWS credentials needed
- Production: secrets encrypted, IAM-controlled, audited
- Test/staging: can use either (flexibility)

**Consequences:**
- `APP_ENV` must be set via environment (chicken-egg: can't load from SSM)
- SSM parameters named `/ky27/prod/database_url` → `DATABASE_URL`
- ECS task needs IAM permission for `ssm:GetParametersByPath`

---
