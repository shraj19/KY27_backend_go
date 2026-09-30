package payment

import (
	"context"
	"fmt"
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

// ProviderFactory creates a PaymentGateway from a generic config map.
// Each provider registers its factory; the central config loader calls it.
type ProviderFactory func(cfg map[string]string) (PaymentGateway, error)

// registry holds registered provider factories. Open/Closed: add providers
// by calling Register, not by modifying this file.
var registry = map[string]ProviderFactory{}

// Register adds a provider factory. Called by each provider's init().
func Register(name string, factory ProviderFactory) {
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("payment: provider %q already registered", name))
	}
	registry[name] = factory
}

// NewGateway creates the gateway for the named provider using the given config.
// The config map contains provider-specific key-value pairs.
func NewGateway(provider string, cfg map[string]string) (PaymentGateway, error) {
	factory, ok := registry[provider]
	if !ok {
		return nil, fmt.Errorf("payment: unknown provider %q", provider)
	}
	return factory(cfg)
}

// RegisteredProviders returns the names of all registered providers.
func RegisteredProviders() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
