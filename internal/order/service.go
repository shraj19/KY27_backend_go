// Package order runs the order-creation pipeline: idempotency, price
// resolution, coupon validation, ownership check, locking, gateway order
// creation, and persistence. UserID is taken from the authenticated request.
package order

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"ky27/backend/internal/catalog"
	"ky27/backend/internal/db"
	"ky27/backend/internal/payment"
)

// Sentinel errors for HTTP status mapping.
var (
	ErrAlreadyOwnsPass     = errors.New("user already owns a pass")
	ErrCouponNotApplicable = errors.New("coupon not applicable")
	ErrLocked              = errors.New("a payment is already in progress for this user")
	ErrIdempotencyMismatch = errors.New("idempotency key reused with a different request")
)

// Request is the input to create an order.
type Request struct {
	UserID         string
	PassID         string
	CouponID       string
	IdempotencyKey string
}

// Result is returned to drive checkout.
type Result struct {
	OrderID          string
	PaymentSessionID string
	AmountPaise      int64
	Status           payment.Status
}

// Service creates orders.
type Service struct {
	catalog catalog.Client
	gateway payment.PaymentGateway
	queries *db.Queries
}

func NewService(cat catalog.Client, gw payment.PaymentGateway, q *db.Queries) *Service {
	return &Service{catalog: cat, gateway: gw, queries: q}
}

// Create creates a payment order for req.
func (s *Service) Create(ctx context.Context, req Request, provider string) (Result, error) {
	bodyHash := hashRequest(req)

	// Return the existing order if this idempotency key was already used.
	if existing, err := s.queries.FindIdempotencyKey(ctx, req.IdempotencyKey); err == nil {
		if existing.BodyHash != bodyHash {
			return Result{}, ErrIdempotencyMismatch
		}
		order, err := s.queries.GetOrder(ctx, existing.OrderID)
		if err != nil {
			return Result{}, fmt.Errorf("order: load idempotent order: %w", err)
		}
		return Result{OrderID: order.ID, AmountPaise: order.AmountPaise, Status: payment.Status(order.Status)}, nil
	}

	// Early rejection; the partial-unique index on orders(user_id) WHERE
	// status='PAID' enforces this at the database level.
	hasPass, err := s.queries.UserHasPaidPass(ctx, req.UserID)
	if err != nil {
		return Result{}, fmt.Errorf("order: ownership check: %w", err)
	}
	if hasPass {
		return Result{}, ErrAlreadyOwnsPass
	}

	// Resolve the price from the catalog.
	pass, err := s.catalog.GetPass(ctx, req.PassID)
	if err != nil {
		return Result{}, fmt.Errorf("order: resolve pass: %w", err)
	}
	amount := pass.PricePaise

	// Apply an optional coupon.
	var couponID *string
	if req.CouponID != "" {
		coupon, err := s.catalog.GetCoupon(ctx, req.CouponID, req.UserID)
		if err != nil {
			return Result{}, fmt.Errorf("order: resolve coupon: %w", err)
		}
		if !coupon.Valid || !coupon.AppliesToUser {
			return Result{}, ErrCouponNotApplicable
		}
		amount -= coupon.DiscountPaise
		if amount < 0 {
			amount = 0
		}
		couponID = &req.CouponID
	}

	orderID := "KY27-" + uuid.NewString()
	expiry := time.Now().Add(s.gateway.OrderExpiry())

	// Acquire the per-user lock (steals only an expired one).
	lock, err := s.queries.UpsertPaymentLock(ctx, db.UpsertPaymentLockParams{
		UserID: req.UserID, OrderID: orderID, ExpiresAt: expiry,
	})
	if err != nil || lock.OrderID != orderID {
		return Result{}, ErrLocked
	}

	// Create the order at the gateway.
	created, err := s.gateway.CreateOrder(ctx, payment.Order{
		ID:          orderID,
		AmountPaise: amount,
		Currency:    "INR",
		Customer:    payment.Customer{ID: req.UserID},
	})
	if err != nil {
		_ = s.queries.DeletePaymentLock(ctx, req.UserID)
		return Result{}, fmt.Errorf("order: gateway create: %w", err)
	}

	// Persist the order and the idempotency key.
	provOrderID := created.ProviderOrderID
	if _, err := s.queries.CreateOrder(ctx, db.CreateOrderParams{
		ID: orderID, UserID: req.UserID, PassID: req.PassID, CouponID: couponID,
		AmountPaise: amount, Currency: "INR", Status: string(payment.StatusActive),
		Provider: provider, ProviderOrderID: &provOrderID, ExpiresAt: expiry,
	}); err != nil {
		return Result{}, fmt.Errorf("order: persist: %w", err)
	}
	if _, err := s.queries.InsertIdempotencyKey(ctx, db.InsertIdempotencyKeyParams{
		Key: req.IdempotencyKey, BodyHash: bodyHash, OrderID: orderID,
	}); err != nil {
		return Result{}, fmt.Errorf("order: persist idempotency: %w", err)
	}

	return Result{
		OrderID:          orderID,
		PaymentSessionID: created.PaymentSessionID,
		AmountPaise:      amount,
		Status:           payment.StatusActive,
	}, nil
}

// hashRequest binds an idempotency key to its request payload.
func hashRequest(r Request) string {
	sum := sha256.Sum256([]byte(r.UserID + "|" + r.PassID + "|" + r.CouponID))
	return hex.EncodeToString(sum[:])
}
