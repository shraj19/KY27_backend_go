package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// CashfreeConfig holds Cashfree provider configuration.
type CashfreeConfig struct {
	ClientID     string `env:"CASHFREE_CLIENT_ID,required"`
	ClientSecret string `env:"CASHFREE_CLIENT_SECRET,required"`
	Environment  string `env:"CASHFREE_ENV" envDefault:"SANDBOX"` // SANDBOX | PRODUCTION
	ReturnURL    string `env:"CASHFREE_RETURN_URL"`
	NotifyURL    string `env:"CASHFREE_NOTIFY_URL"`
}

// LoadCashfreeConfig loads Cashfree config from environment.
func LoadCashfreeConfig() (CashfreeConfig, error) {
	var cfg CashfreeConfig
	if err := env.Parse(&cfg); err != nil {
		return CashfreeConfig{}, fmt.Errorf("cashfree config: %w", err)
	}
	return cfg, nil
}

// Validate checks required fields and valid values.
func (c CashfreeConfig) Validate() error {
	if c.Environment != "SANDBOX" && c.Environment != "PRODUCTION" {
		return fmt.Errorf("cashfree: CASHFREE_ENV must be SANDBOX or PRODUCTION, got %q", c.Environment)
	}
	return nil
}

// RazorpayConfig holds Razorpay provider configuration (future).
type RazorpayConfig struct {
	KeyID         string `env:"RAZORPAY_KEY_ID,required"`
	KeySecret     string `env:"RAZORPAY_KEY_SECRET,required"`
	WebhookSecret string `env:"RAZORPAY_WEBHOOK_SECRET"`
}

// LoadRazorpayConfig loads Razorpay config from environment.
func LoadRazorpayConfig() (RazorpayConfig, error) {
	var cfg RazorpayConfig
	if err := env.Parse(&cfg); err != nil {
		return RazorpayConfig{}, fmt.Errorf("razorpay config: %w", err)
	}
	return cfg, nil
}
