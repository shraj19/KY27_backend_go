package payment

import (
	"context"
	"fmt"
	"time"

	cashfree "github.com/cashfree/cashfree-pg/v5"
)

// CashfreeConfig holds Cashfree credentials and callback URLs.
type CashfreeConfig struct {
	Environment  string
	ClientID     string
	ClientSecret string
	ReturnURL    string
	NotifyURL    string
}

type cashfreeProvider struct {
	client    *cashfree.Cashfree
	returnURL string
	notifyURL string
}

const cashfreeOrderExpiry = 30 * time.Minute

var _ PaymentGateway = (*cashfreeProvider)(nil)

func (c *cashfreeProvider) OrderExpiry() time.Duration {
	return cashfreeOrderExpiry
}

// NewCashfreeProvider builds a Cashfree gateway from cfg.
func NewCashfreeProvider(cfg CashfreeConfig) (PaymentGateway, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("cashfree: missing client id or secret")
	}

	env := cashfree.SANDBOX
	if cfg.Environment == "PRODUCTION" {
		env = cashfree.PRODUCTION
	}

	clientID := cfg.ClientID
	clientSecret := cfg.ClientSecret
	client := &cashfree.Cashfree{
		XClientId:     &clientID,
		XClientSecret: &clientSecret,
		XEnvironment:  &env,
	}

	return &cashfreeProvider{
		client:    client,
		returnURL: cfg.ReturnURL,
		notifyURL: cfg.NotifyURL,
	}, nil
}

// CreateOrder creates an order with Cashfree and returns the payment session.
func (c *cashfreeProvider) CreateOrder(ctx context.Context, o Order) (CreatedOrder, error) {
	currency := o.Currency
	if currency == "" {
		currency = "INR"
	}

	amountRupees := float64(o.AmountPaise) / 100.0

	orderID := o.ID
	name := o.Customer.Name
	email := o.Customer.Email
	expiry := time.Now().Add(cashfreeOrderExpiry).Format(time.RFC3339)

	req := cashfree.CreateOrderRequest{
		OrderId:       &orderID,
		OrderAmount:   amountRupees,
		OrderCurrency: currency,
		CustomerDetails: cashfree.CustomerDetails{
			CustomerId:    o.Customer.ID,
			CustomerPhone: o.Customer.Phone,
			CustomerName:  &name,
			CustomerEmail: &email,
		},
		OrderMeta: &cashfree.OrderMeta{
			ReturnUrl: &c.returnURL,
			NotifyUrl: &c.notifyURL,
		},
		OrderExpiryTime: &expiry,
	}

	resp, _, err := c.client.PGCreateOrderWithContext(ctx, &req, nil, nil, nil)
	if err != nil {
		return CreatedOrder{}, fmt.Errorf("cashfree: create order: %w", err)
	}

	return CreatedOrder{
		OrderID:          derefStr(resp.OrderId),
		ProviderOrderID:  derefStr(resp.CfOrderId),
		PaymentSessionID: derefStr(resp.PaymentSessionId),
		Status:           mapCashfreeStatus(derefStr(resp.OrderStatus)),
	}, nil
}

// VerifyPayment returns the current status of an order at Cashfree.
func (c *cashfreeProvider) VerifyPayment(ctx context.Context, orderID string) (Status, error) {
	resp, _, err := c.client.PGFetchOrderWithContext(ctx, orderID, nil, nil, nil)
	if err != nil {
		return StatusUnknown, fmt.Errorf("cashfree: verify payment: %w", err)
	}
	return mapCashfreeStatus(derefStr(resp.OrderStatus)), nil
}

// mapCashfreeStatus maps a Cashfree status to Status.
func mapCashfreeStatus(s string) Status {
	switch s {
	case "PAID":
		return StatusPaid
	case "ACTIVE":
		return StatusActive
	case "EXPIRED":
		return StatusExpired
	case "TERMINATED", "TERMINATION_REQUESTED":
		return StatusFailed
	default:
		return StatusUnknown
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
