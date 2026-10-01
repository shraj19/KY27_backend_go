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

// PaymentGateway abstracts a payment provider.
type PaymentGateway interface {
	CreateOrder(ctx context.Context, o Order) (CreatedOrder, error)
	VerifyPayment(ctx context.Context, orderID string) (Status, error)
	OrderExpiry() time.Duration
}
