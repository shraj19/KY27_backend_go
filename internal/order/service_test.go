package order

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"ky27/backend/internal/db"
	"ky27/backend/internal/metrics"
)

func TestMain(m *testing.M) {
	// Initialize metrics so RecordPaymentSuccess doesn't panic
	_ = metrics.Init()
	os.Exit(m.Run())
}

// --- Mocks ---

// mockTx implements pgx.Tx for testing.
type mockTx struct {
	committed  bool
	rolledBack bool
}

func (m *mockTx) Begin(ctx context.Context) (pgx.Tx, error)            { return m, nil }
func (m *mockTx) Commit(ctx context.Context) error                     { m.committed = true; return nil }
func (m *mockTx) Rollback(ctx context.Context) error                   { m.rolledBack = true; return nil }
func (m *mockTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (m *mockTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults { return nil }
func (m *mockTx) LargeObjects() pgx.LargeObjects                               { return pgx.LargeObjects{} }
func (m *mockTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (m *mockTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (m *mockTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}
func (m *mockTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }
func (m *mockTx) Conn() *pgx.Conn                                               { return nil }

// mockPool implements TxBeginner.
type mockPool struct {
	tx *mockTx
}

func (m *mockPool) Begin(ctx context.Context) (pgx.Tx, error) {
	return m.tx, nil
}

// mockJobs implements JobInserter.
type mockJobs struct {
	inserted []jobRecord
	err      error
}

type jobRecord struct {
	OrderID     string
	Status      string
	AmountPaise int64
	PaidAt      string
}

func (m *mockJobs) InsertTx(ctx context.Context, tx pgx.Tx, orderID, status string, amountPaise int64, paidAt string) error {
	if m.err != nil {
		return m.err
	}
	m.inserted = append(m.inserted, jobRecord{orderID, status, amountPaise, paidAt})
	return nil
}

// mockQuerier implements db.Querier for testing.
type mockQuerier struct {
	orders          map[string]db.PaymentsOrder
	markPaidCalled  bool
	markPaidErr     error
	getStatusResult *db.GetOrderStatusRow
	getStatusErr    error
}

func newMockQuerier() *mockQuerier {
	return &mockQuerier{orders: make(map[string]db.PaymentsOrder)}
}

func (m *mockQuerier) MarkOrderPaidIfActive(ctx context.Context, arg db.MarkOrderPaidIfActiveParams) (string, error) {
	m.markPaidCalled = true
	if m.markPaidErr != nil {
		return "", m.markPaidErr
	}
	// Simulate: return order ID if transition happened
	order, exists := m.orders[arg.ID]
	if !exists {
		return "", pgx.ErrNoRows
	}
	if order.Status != "ACTIVE" {
		return "", pgx.ErrNoRows
	}
	if order.TotalPaise != arg.TotalPaise {
		return "", pgx.ErrNoRows
	}
	// Transition
	order.Status = "PAID"
	m.orders[arg.ID] = order
	return arg.ID, nil
}

func (m *mockQuerier) GetOrderStatus(ctx context.Context, id string) (db.GetOrderStatusRow, error) {
	if m.getStatusErr != nil {
		return db.GetOrderStatusRow{}, m.getStatusErr
	}
	if m.getStatusResult != nil {
		return *m.getStatusResult, nil
	}
	order, exists := m.orders[id]
	if !exists {
		return db.GetOrderStatusRow{}, pgx.ErrNoRows
	}
	return db.GetOrderStatusRow{ID: order.ID, Status: order.Status, TotalPaise: order.TotalPaise}, nil
}

// Unused methods — satisfy interface
func (m *mockQuerier) CreateOrder(ctx context.Context, arg db.CreateOrderParams) (db.PaymentsOrder, error) {
	return db.PaymentsOrder{}, nil
}
func (m *mockQuerier) FindIdempotencyKey(ctx context.Context, key string) (db.PaymentsIdempotencyKey, error) {
	return db.PaymentsIdempotencyKey{}, pgx.ErrNoRows
}
func (m *mockQuerier) GetOrder(ctx context.Context, id string) (db.PaymentsOrder, error) {
	return db.PaymentsOrder{}, nil
}
func (m *mockQuerier) GetOrderByProviderOrderID(ctx context.Context, providerOrderID *string) (db.PaymentsOrder, error) {
	return db.PaymentsOrder{}, nil
}
func (m *mockQuerier) InsertIdempotencyKey(ctx context.Context, arg db.InsertIdempotencyKeyParams) (db.PaymentsIdempotencyKey, error) {
	return db.PaymentsIdempotencyKey{}, nil
}
func (m *mockQuerier) UpdateOrderStatus(ctx context.Context, arg db.UpdateOrderStatusParams) (db.PaymentsOrder, error) {
	return db.PaymentsOrder{}, nil
}

// --- Tests ---

func TestComplete_Success(t *testing.T) {
	tx := &mockTx{}
	pool := &mockPool{tx: tx}
	jobs := &mockJobs{}
	queries := newMockQuerier()

	// Setup: order exists, ACTIVE, correct amount
	queries.orders["order-123"] = db.PaymentsOrder{
		ID:         "order-123",
		Status:     "ACTIVE",
		TotalPaise: 50000,
	}

	svc := NewService(nil, pool, queries, jobs, "test").
		WithQuerierFactory(func(tx pgx.Tx) db.Querier { return queries })

	err := svc.Complete(context.Background(), CompleteRequest{
		OrderID:     "order-123",
		AmountPaise: 50000,
		PaidAt:      time.Now(),
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify transaction committed
	if !tx.committed {
		t.Error("expected transaction to be committed")
	}

	// Verify job was enqueued
	if len(jobs.inserted) != 1 {
		t.Fatalf("expected 1 job inserted, got %d", len(jobs.inserted))
	}
	if jobs.inserted[0].OrderID != "order-123" {
		t.Errorf("expected order-123, got %s", jobs.inserted[0].OrderID)
	}

	// Verify order status changed
	if queries.orders["order-123"].Status != "PAID" {
		t.Errorf("expected PAID, got %s", queries.orders["order-123"].Status)
	}
}

func TestComplete_DuplicateWebhook(t *testing.T) {
	tx := &mockTx{}
	pool := &mockPool{tx: tx}
	jobs := &mockJobs{}
	queries := newMockQuerier()

	// Setup: order already PAID
	queries.orders["order-123"] = db.PaymentsOrder{
		ID:         "order-123",
		Status:     "PAID", // Already paid!
		TotalPaise: 50000,
	}

	svc := NewService(nil, pool, queries, jobs, "test").
		WithQuerierFactory(func(tx pgx.Tx) db.Querier { return queries })

	err := svc.Complete(context.Background(), CompleteRequest{
		OrderID:     "order-123",
		AmountPaise: 50000,
		PaidAt:      time.Now(),
	})

	// Should succeed (idempotent)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// NO job should be enqueued
	if len(jobs.inserted) != 0 {
		t.Errorf("expected 0 jobs inserted for duplicate, got %d", len(jobs.inserted))
	}
}

func TestComplete_AmountMismatch(t *testing.T) {
	tx := &mockTx{}
	pool := &mockPool{tx: tx}
	jobs := &mockJobs{}
	queries := newMockQuerier()

	// Setup: order expects 50000 paise
	queries.orders["order-123"] = db.PaymentsOrder{
		ID:         "order-123",
		Status:     "ACTIVE",
		TotalPaise: 50000,
	}

	svc := NewService(nil, pool, queries, jobs, "test").
		WithQuerierFactory(func(tx pgx.Tx) db.Querier { return queries })

	// Webhook says 5000 paise (wrong!)
	err := svc.Complete(context.Background(), CompleteRequest{
		OrderID:     "order-123",
		AmountPaise: 5000, // Mismatch!
		PaidAt:      time.Now(),
	})

	// Should succeed (we don't error, just log)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// NO job should be enqueued
	if len(jobs.inserted) != 0 {
		t.Errorf("expected 0 jobs for amount mismatch, got %d", len(jobs.inserted))
	}

	// Order should still be ACTIVE (not marked paid)
	if queries.orders["order-123"].Status != "ACTIVE" {
		t.Errorf("expected ACTIVE, got %s", queries.orders["order-123"].Status)
	}
}

func TestComplete_UnknownOrder(t *testing.T) {
	tx := &mockTx{}
	pool := &mockPool{tx: tx}
	jobs := &mockJobs{}
	queries := newMockQuerier()

	// No orders exist

	svc := NewService(nil, pool, queries, jobs, "test").
		WithQuerierFactory(func(tx pgx.Tx) db.Querier { return queries })

	err := svc.Complete(context.Background(), CompleteRequest{
		OrderID:     "nonexistent",
		AmountPaise: 50000,
		PaidAt:      time.Now(),
	})

	// Should succeed (idempotent — don't error on unknown)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// NO job should be enqueued
	if len(jobs.inserted) != 0 {
		t.Errorf("expected 0 jobs for unknown order, got %d", len(jobs.inserted))
	}
}

func TestComplete_JobInsertFails(t *testing.T) {
	tx := &mockTx{}
	pool := &mockPool{tx: tx}
	jobs := &mockJobs{err: errors.New("river down")}
	queries := newMockQuerier()

	queries.orders["order-123"] = db.PaymentsOrder{
		ID:         "order-123",
		Status:     "ACTIVE",
		TotalPaise: 50000,
	}

	svc := NewService(nil, pool, queries, jobs, "test").
		WithQuerierFactory(func(tx pgx.Tx) db.Querier { return queries })

	err := svc.Complete(context.Background(), CompleteRequest{
		OrderID:     "order-123",
		AmountPaise: 50000,
		PaidAt:      time.Now(),
	})

	// Should error
	if err == nil {
		t.Fatal("expected error when job insert fails")
	}

	// Transaction should NOT be committed
	if tx.committed {
		t.Error("expected transaction to NOT be committed on job failure")
	}
}
