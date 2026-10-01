package config

import (
	"fmt"
	"os"
)

// CashfreeConfig holds Cashfree provider configuration.
type CashfreeConfig struct {
	ClientID     string
	ClientSecret string
	Environment  string // "SANDBOX" | "PRODUCTION"
	ReturnURL    string
	NotifyURL    string
}

// LoadCashfreeConfig loads Cashfree provider config from environment.
func LoadCashfreeConfig() CashfreeConfig {
	return CashfreeConfig{
		ClientID:     os.Getenv("CASHFREE_CLIENT_ID"),
		ClientSecret: os.Getenv("CASHFREE_CLIENT_SECRET"),
		Environment:  os.Getenv("CASHFREE_ENV"),
		ReturnURL:    os.Getenv("CASHFREE_RETURN_URL"),
		NotifyURL:    os.Getenv("CASHFREE_NOTIFY_URL"),
	}
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
