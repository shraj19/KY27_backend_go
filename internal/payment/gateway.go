package payment

import (
	"context"
	"time"
)

// Status is a payment/order status.
type Status string

const (
	StatusActive  Status = "ACTIVE"
	StatusPaid    Status = "PAID"
	StatusExpired Status = "EXPIRED"
	StatusFailed  Status = "FAILED"
	StatusUnknown Status = "UNKNOWN"
)

// Customer identifies who is being charged.
type Customer struct {
	ID    string
	Phone string
	Name  string
	Email string
}

// Order is the request to create a payment. AmountPaise is in integer paise.
type Order struct {
	ID          string
	AmountPaise int64
	Currency    string
	Customer    Customer
}

// CreatedOrder is the result of creating an order with a gateway.
type CreatedOrder struct {
	OrderID          string
	ProviderOrderID  string
	PaymentSessionID string
	Status           Status
}

// WebhookEvent is the parsed result of a payment webhook.
type WebhookEvent struct {
	OrderID     string
	Status      Status
	AmountPaise int64
	PaidAt      time.Time
	RawPayload  []byte // Original payload for audit
}

// WebhookHeaders defines which HTTP headers contain signature data.
type WebhookHeaders struct {
	Signature string // Header name for signature (required)
	Timestamp string // Header name for timestamp (empty if not used)
}

// PaymentGateway abstracts a payment provider.
type PaymentGateway interface {
	CreateOrder(ctx context.Context, o Order) (CreatedOrder, error)
	VerifyPayment(ctx context.Context, orderID string) (Status, error)
	OrderExpiry() time.Duration

	// WebhookHeaders returns the header names this provider uses for webhooks.
	WebhookHeaders() WebhookHeaders

	// VerifyWebhook validates the webhook signature and parses the event.
	VerifyWebhook(signature string, body []byte, timestamp string) (WebhookEvent, error)
}
