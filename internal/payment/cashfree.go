package payment

import (
	"context"
	"fmt"
	"time"

	cashfree "github.com/cashfree/cashfree-pg/v5"
)

// CashfreeConfig defines what the Cashfree provider needs.
// Exported so config.Load() can populate it.
type CashfreeConfig struct {
	ClientID     string
	ClientSecret string
	Environment  string // "SANDBOX" | "PRODUCTION"
	ReturnURL    string
	NotifyURL    string
}

// ConfigKeys returns the env var names this provider expects.
// Used by config loader to know what to read.
func (CashfreeConfig) ConfigKeys() []string {
	return []string{
		"CASHFREE_CLIENT_ID",
		"CASHFREE_CLIENT_SECRET",
		"CASHFREE_ENV",
		"CASHFREE_RETURN_URL",
		"CASHFREE_NOTIFY_URL",
	}
}

// FromMap populates config from a string map (from env loader).
func (c *CashfreeConfig) FromMap(m map[string]string) {
	c.ClientID = m["CASHFREE_CLIENT_ID"]
	c.ClientSecret = m["CASHFREE_CLIENT_SECRET"]
	c.Environment = m["CASHFREE_ENV"]
	c.ReturnURL = m["CASHFREE_RETURN_URL"]
	c.NotifyURL = m["CASHFREE_NOTIFY_URL"]
}

// Validate checks required fields.
func (c CashfreeConfig) Validate() error {
	if c.ClientID == "" || c.ClientSecret == "" {
		return fmt.Errorf("cashfree: CASHFREE_CLIENT_ID and CASHFREE_CLIENT_SECRET required")
	}
	if c.Environment != "SANDBOX" && c.Environment != "PRODUCTION" {
		return fmt.Errorf("cashfree: CASHFREE_ENV must be SANDBOX or PRODUCTION, got %q", c.Environment)
	}
	return nil
}

func init() {
	Register("cashfree", newCashfreeFromMap)
}

func newCashfreeFromMap(m map[string]string) (PaymentGateway, error) {
	var cfg CashfreeConfig
	cfg.FromMap(m)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return NewCashfreeProvider(cfg)
}

// NewCashfreeProvider builds a Cashfree gateway from typed config.
// Exported for direct use in tests with fake config.
func NewCashfreeProvider(cfg CashfreeConfig) (PaymentGateway, error) {
	env := cashfree.SANDBOX
	if cfg.Environment == "PRODUCTION" {
		env = cashfree.PRODUCTION
	}

	client := &cashfree.Cashfree{
		XClientId:     &cfg.ClientID,
		XClientSecret: &cfg.ClientSecret,
		XEnvironment:  &env,
	}

	return &cashfreeProvider{
		client:    client,
		returnURL: cfg.ReturnURL,
		notifyURL: cfg.NotifyURL,
	}, nil
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
