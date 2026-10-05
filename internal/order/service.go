// Package order owns the payment order lifecycle: create, complete, fail, expire.
package order

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"ky27/backend/internal/db"
	"ky27/backend/internal/metrics"
	"ky27/backend/internal/payment"
)

var (
	ErrIdempotencyMismatch = errors.New("idempotency key reused with different request")
	ErrOrderNotFound       = errors.New("order not found")
	ErrAmountMismatch      = errors.New("payment amount does not match order")
	ErrInvalidTransition   = errors.New("order cannot transition from current state")
)

// Buyer identifies who is paying.
type Buyer struct {
	ID    string
	Phone string
	Email string
	Name  string
}

// CreateRequest is a pre-priced order from Node.
type CreateRequest struct {
	OrderID        string
	Buyer          Buyer
	TotalPaise     int64
	Currency       string
	IdempotencyKey string
}

// CreateResult is returned to Node to complete checkout.
type CreateResult struct {
	OrderID          string
	PaymentSessionID string
	Status           payment.Status
}

// CompleteRequest contains payment completion details from webhook.
type CompleteRequest struct {
	OrderID           string
	AmountPaise       int64
	PaidAt            time.Time
	ProviderPaymentID string // For logging/debugging
}

// TxBeginner starts a database transaction.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// JobInserter enqueues async jobs within a transaction.
type JobInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, orderID string, status string, amountPaise int64, paidAt string) error
}

// QuerierFactory creates a Querier from a transaction. Allows test injection.
type QuerierFactory func(tx pgx.Tx) db.Querier

// Service manages the order lifecycle.
type Service struct {
	gateway        payment.PaymentGateway
	pool           TxBeginner
	queries        db.Querier      // For non-transactional reads
	querierFactory QuerierFactory  // For transactional operations
	jobs           JobInserter
	provider       string
}

func NewService(gw payment.PaymentGateway, pool TxBeginner, q db.Querier, jobs JobInserter, provider string) *Service {
	return &Service{
		gateway:        gw,
		pool:           pool,
		queries:        q,
		querierFactory: func(tx pgx.Tx) db.Querier { return db.New(tx) }, // Default: real db.Queries
		jobs:           jobs,
		provider:       provider,
	}
}

// WithQuerierFactory sets a custom querier factory (for testing).
func (s *Service) WithQuerierFactory(f QuerierFactory) *Service {
	s.querierFactory = f
	return s
}

// Create creates a new payment order. Returns existing order on idempotency hit.
func (s *Service) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	bodyHash := hashRequest(req)

	// Idempotency check
	if existing, err := s.queries.FindIdempotencyKey(ctx, req.IdempotencyKey); err == nil {
		if existing.BodyHash != bodyHash {
			slog.WarnContext(ctx, "idempotency mismatch", "key", req.IdempotencyKey)
			return CreateResult{}, ErrIdempotencyMismatch
		}
		order, err := s.queries.GetOrder(ctx, existing.OrderID)
		if err != nil {
			return CreateResult{}, fmt.Errorf("load idempotent order: %w", err)
		}
		slog.DebugContext(ctx, "idempotency hit", "order_id", order.ID)
		return CreateResult{OrderID: order.ID, Status: payment.Status(order.Status)}, nil
	}

	orderID := req.OrderID
	if orderID == "" {
		orderID = "KY27-" + uuid.NewString()
	}

	currency := req.Currency
	if currency == "" {
		currency = "INR"
	}

	// Create at gateway
	created, err := s.gateway.CreateOrder(ctx, payment.Order{
		ID:          orderID,
		AmountPaise: req.TotalPaise,
		Currency:    currency,
		Customer: payment.Customer{
			ID:    req.Buyer.ID,
			Phone: req.Buyer.Phone,
			Email: req.Buyer.Email,
			Name:  req.Buyer.Name,
		},
	})
	if err != nil {
		return CreateResult{}, fmt.Errorf("gateway create: %w", err)
	}

	// Persist order
	provOrderID := created.ProviderOrderID
	if _, err := s.queries.CreateOrder(ctx, db.CreateOrderParams{
		ID:              orderID,
		BuyerID:         req.Buyer.ID,
		TotalPaise:      req.TotalPaise,
		Currency:        currency,
		Status:          string(payment.StatusActive),
		Provider:        s.provider,
		ProviderOrderID: &provOrderID,
		ExpiresAt:       time.Now().Add(s.gateway.OrderExpiry()),
		BuyerPhone:      req.Buyer.Phone,
		BuyerEmail:      req.Buyer.Email,
		BuyerName:       req.Buyer.Name,
	}); err != nil {
		return CreateResult{}, fmt.Errorf("persist order: %w", err)
	}

	// Store idempotency key
	if _, err := s.queries.InsertIdempotencyKey(ctx, db.InsertIdempotencyKeyParams{
		Key:      req.IdempotencyKey,
		BodyHash: bodyHash,
		OrderID:  orderID,
	}); err != nil {
		return CreateResult{}, fmt.Errorf("persist idempotency: %w", err)
	}

	slog.InfoContext(ctx, "order created", "order_id", orderID, "buyer_id", req.Buyer.ID, "amount_paise", req.TotalPaise)

	return CreateResult{
		OrderID:          orderID,
		PaymentSessionID: created.PaymentSessionID,
		Status:           payment.StatusActive,
	}, nil
}

// Complete transitions ACTIVE → PAID and enqueues notification. Idempotent.
func (s *Service) Complete(ctx context.Context, req CompleteRequest) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	queries := s.querierFactory(tx)
	paidAt := pgtype.Timestamptz{Time: req.PaidAt, Valid: !req.PaidAt.IsZero()}

	// Idempotent transition: only succeeds if ACTIVE + amount matches
	_, err = queries.MarkOrderPaidIfActive(ctx, db.MarkOrderPaidIfActiveParams{
		ID:         req.OrderID,
		PaidAt:     paidAt,
		TotalPaise: req.AmountPaise,
	})

	if errors.Is(err, pgx.ErrNoRows) {
		return s.handleNoTransition(ctx, queries, req)
	}
	if err != nil {
		return fmt.Errorf("mark paid: %w", err)
	}

	// Enqueue notification (only happens once per order due to idempotent transition)
	if err := s.jobs.InsertTx(ctx, tx, req.OrderID, string(payment.StatusPaid), req.AmountPaise, req.PaidAt.Format(time.RFC3339)); err != nil {
		return fmt.Errorf("enqueue notification: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.InfoContext(ctx, "order completed", "order_id", req.OrderID, "amount_paise", req.AmountPaise, "provider_payment_id", req.ProviderPaymentID)
	metrics.RecordPaymentSuccess(ctx, req.AmountPaise)
	return nil
}

// handleNoTransition figures out why Complete didn't transition and logs appropriately.
func (s *Service) handleNoTransition(ctx context.Context, queries db.Querier, req CompleteRequest) error {
	order, err := queries.GetOrderStatus(ctx, req.OrderID)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.WarnContext(ctx, "complete called for unknown order", "order_id", req.OrderID, "provider_payment_id", req.ProviderPaymentID)
		return nil // Idempotent — don't error on unknown order
	}
	if err != nil {
		return err
	}

	if order.Status == "PAID" {
		slog.DebugContext(ctx, "order already paid", "order_id", req.OrderID)
		return nil // Idempotent success
	}

	if order.TotalPaise != req.AmountPaise {
		slog.ErrorContext(ctx, "amount mismatch", "order_id", req.OrderID, "expected", order.TotalPaise, "got", req.AmountPaise, "provider_payment_id", req.ProviderPaymentID)
		metrics.RecordPaymentFailed(ctx, "amount_mismatch")
		return nil // Don't retry — needs manual review
	}

	slog.WarnContext(ctx, "complete called for non-active order", "order_id", req.OrderID, "status", order.Status)
	return nil
}

// Fail transitions ACTIVE → FAILED.
func (s *Service) Fail(ctx context.Context, orderID string, reason string) error {
	order, err := s.queries.GetOrderStatus(ctx, orderID)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}

	if order.Status != "ACTIVE" {
		slog.DebugContext(ctx, "fail called for non-active order", "order_id", orderID, "status", order.Status)
		return nil // Idempotent
	}

	if _, err := s.queries.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
		ID:     orderID,
		Status: string(payment.StatusFailed),
	}); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	slog.InfoContext(ctx, "order failed", "order_id", orderID, "reason", reason)
	metrics.RecordPaymentFailed(ctx, reason)
	return nil
}

// Expire transitions ACTIVE → EXPIRED for orders past their expiry time.
func (s *Service) Expire(ctx context.Context, orderID string) error {
	order, err := s.queries.GetOrderStatus(ctx, orderID)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}

	if order.Status != "ACTIVE" {
		slog.DebugContext(ctx, "expire called for non-active order", "order_id", orderID, "status", order.Status)
		return nil // Idempotent
	}

	if _, err := s.queries.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
		ID:     orderID,
		Status: string(payment.StatusExpired),
	}); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	slog.InfoContext(ctx, "order expired", "order_id", orderID)
	metrics.RecordPaymentFailed(ctx, "expired")
	return nil
}

// GetStatus returns current order status.
func (s *Service) GetStatus(ctx context.Context, orderID string) (payment.Status, error) {
	order, err := s.queries.GetOrder(ctx, orderID)
	if err != nil {
		return payment.StatusUnknown, fmt.Errorf("get order: %w", err)
	}
	return payment.Status(order.Status), nil
}

func hashRequest(r CreateRequest) string {
	data := fmt.Sprintf("%s|%s|%d|%s", r.OrderID, r.Buyer.ID, r.TotalPaise, r.IdempotencyKey)
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
