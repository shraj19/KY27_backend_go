package payment

import (
	"context"
	"fmt"
	"time"

	"ky27/backend/internal/config"

	cashfree "github.com/cashfree/cashfree-pg/v5"
)

// -----------------------------------------------------------------------------
// Constants
// -----------------------------------------------------------------------------

const cashfreeOrderExpiry = 30 * time.Minute

// -----------------------------------------------------------------------------
// Provider
// -----------------------------------------------------------------------------

type cashfreeProvider struct {
	client    *cashfree.Cashfree
	returnURL string
	notifyURL string
}

// Compile-time check that cashfreeProvider implements PaymentGateway.
var _ PaymentGateway = (*cashfreeProvider)(nil)

// -----------------------------------------------------------------------------
// Constructor
// -----------------------------------------------------------------------------

// NewCashfreeProvider builds a Cashfree gateway from config.
func NewCashfreeProvider(cfg config.CashfreeConfig) (PaymentGateway, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

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

// -----------------------------------------------------------------------------
// PaymentGateway implementation
// -----------------------------------------------------------------------------

func (p *cashfreeProvider) OrderExpiry() time.Duration {
	return cashfreeOrderExpiry
}

func (p *cashfreeProvider) CreateOrder(ctx context.Context, o Order) (CreatedOrder, error) {
	currency := o.Currency
	if currency == "" {
		currency = "INR"
	}

	amountRupees := float64(o.AmountPaise) / 100.0
	expiry := time.Now().Add(cashfreeOrderExpiry).Format(time.RFC3339)

	req := cashfree.CreateOrderRequest{
		OrderId:       &o.ID,
		OrderAmount:   amountRupees,
		OrderCurrency: currency,
		CustomerDetails: cashfree.CustomerDetails{
			CustomerId:    o.Customer.ID,
			CustomerPhone: o.Customer.Phone,
			CustomerName:  &o.Customer.Name,
			CustomerEmail: &o.Customer.Email,
		},
		OrderMeta: &cashfree.OrderMeta{
			ReturnUrl: &p.returnURL,
			NotifyUrl: &p.notifyURL,
		},
		OrderExpiryTime: &expiry,
	}

	resp, _, err := p.client.PGCreateOrderWithContext(ctx, &req, nil, nil, nil)
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

func (p *cashfreeProvider) VerifyPayment(ctx context.Context, orderID string) (Status, error) {
	resp, _, err := p.client.PGFetchOrderWithContext(ctx, orderID, nil, nil, nil)
	if err != nil {
		return StatusUnknown, fmt.Errorf("cashfree: verify payment: %w", err)
	}
	return mapCashfreeStatus(derefStr(resp.OrderStatus)), nil
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

var cashfreeStatusMap = map[string]Status{
	"PAID":                   StatusPaid,
	"ACTIVE":                 StatusActive,
	"EXPIRED":                StatusExpired,
	"TERMINATED":             StatusFailed,
	"TERMINATION_REQUESTED":  StatusFailed,
}

func mapCashfreeStatus(s string) Status {
	if status, ok := cashfreeStatusMap[s]; ok {
		return status
	}
	return StatusUnknown
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
