// Package order creates payment orders given pre-priced requests from Node.
// Node owns cart, pricing, and fulfillment. This service just collects money.
package order

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"ky27/backend/internal/db"
	"ky27/backend/internal/payment"
)

var (
	ErrIdempotencyMismatch = errors.New("idempotency key reused with a different request")
)

// Buyer identifies who is paying.
type Buyer struct {
	ID    string
	Phone string
	Email string
	Name  string
}

// Item is one line item (one ticket for one attendee).
type Item struct {
	PassID        string
	AmountPaise   int64
	AttendeeName  string
	AttendeeEmail string
	AttendeePhone string
}

// Request is a pre-priced order from Node. Go trusts the total.
type Request struct {
	OrderID        string // Node's order id
	Buyer          Buyer
	Items          []Item // the cart: N tickets for N attendees
	TotalPaise     int64  // trusted total from Node (sum of items + any discounts)
	Currency       string
	IdempotencyKey string
}

// Result is returned to Node to complete checkout.
type Result struct {
	OrderID          string
	PaymentSessionID string
	Status           payment.Status
}

// Service creates payment orders.
type Service struct {
	gateway  payment.PaymentGateway
	queries  *db.Queries
	provider string
}

func NewService(gw payment.PaymentGateway, q *db.Queries, provider string) *Service {
	return &Service{gateway: gw, queries: q, provider: provider}
}

// Create creates a payment order with line items. The total is trusted from Node.
func (s *Service) Create(ctx context.Context, req Request) (Result, error) {
	bodyHash := hashRequest(req)

	// Idempotency: return existing order if key was already used.
	if existing, err := s.queries.FindIdempotencyKey(ctx, req.IdempotencyKey); err == nil {
		if existing.BodyHash != bodyHash {
			return Result{}, ErrIdempotencyMismatch
		}
		order, err := s.queries.GetOrder(ctx, existing.OrderID)
		if err != nil {
			return Result{}, fmt.Errorf("order: load idempotent order: %w", err)
		}
		return Result{
			OrderID: order.ID,
			Status:  payment.Status(order.Status),
		}, nil
	}

	orderID := req.OrderID
	if orderID == "" {
		orderID = "KY27-" + uuid.NewString()
	}

	currency := req.Currency
	if currency == "" {
		currency = "INR"
	}

	expiry := time.Now().Add(s.gateway.OrderExpiry())

	// Create the order at the gateway.
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
		return Result{}, fmt.Errorf("order: gateway create: %w", err)
	}

	// Persist the order.
	provOrderID := created.ProviderOrderID
	if _, err := s.queries.CreateOrder(ctx, db.CreateOrderParams{
		ID:              orderID,
		BuyerID:         req.Buyer.ID,
		TotalPaise:      req.TotalPaise,
		Currency:        currency,
		Status:          string(payment.StatusActive),
		Provider:        s.provider,
		ProviderOrderID: &provOrderID,
		ExpiresAt:       expiry,
		BuyerPhone:      req.Buyer.Phone,
		BuyerEmail:      req.Buyer.Email,
		BuyerName:       req.Buyer.Name,
	}); err != nil {
		return Result{}, fmt.Errorf("order: persist: %w", err)
	}

	// Persist each line item.
	for _, item := range req.Items {
		itemID := uuid.NewString()
		if _, err := s.queries.CreateOrderItem(ctx, db.CreateOrderItemParams{
			ID:            itemID,
			OrderID:       orderID,
			PassID:        item.PassID,
			AmountPaise:   item.AmountPaise,
			AttendeeName:  item.AttendeeName,
			AttendeeEmail: item.AttendeeEmail,
			AttendeePhone: item.AttendeePhone,
		}); err != nil {
			return Result{}, fmt.Errorf("order: persist item: %w", err)
		}
	}

	// Store the idempotency key.
	if _, err := s.queries.InsertIdempotencyKey(ctx, db.InsertIdempotencyKeyParams{
		Key:      req.IdempotencyKey,
		BodyHash: bodyHash,
		OrderID:  orderID,
	}); err != nil {
		return Result{}, fmt.Errorf("order: persist idempotency: %w", err)
	}

	return Result{
		OrderID:          orderID,
		PaymentSessionID: created.PaymentSessionID,
		Status:           payment.StatusActive,
	}, nil
}

func hashRequest(r Request) string {
	data := fmt.Sprintf("%s|%s|%d|%d|%s", r.OrderID, r.Buyer.ID, r.TotalPaise, len(r.Items), r.IdempotencyKey)
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
