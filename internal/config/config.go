package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"ky27/backend/internal/payment"
)

// Config is the fully-loaded, validated application configuration.
type Config struct {
	Port        int
	CorsOrigins []string
	Payment     payment.Config
}

// Load reads .env (if present) and environment variables into a validated Config.
func Load() (Config, error) {
	loadDotEnv(".env")

	cfg := Config{
		Port:        getIntEnv("PORT", 8081),
		CorsOrigins: getSliceEnv("CORS_ORIGINS"),
		Payment: payment.Config{
			Provider: os.Getenv("PAYMENT_PROVIDER"),
			Cashfree: payment.CashfreeConfig{
				Environment:  os.Getenv("CASHFREE_ENV"),
				ClientID:     os.Getenv("CASHFREE_CLIENT_ID"),
				ClientSecret: os.Getenv("CASHFREE_CLIENT_SECRET"),
				ReturnURL:    os.Getenv("CASHFREE_RETURN_URL"),
				NotifyURL:    os.Getenv("CASHFREE_NOTIFY_URL"),
			},
		},
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks configuration invariants.
func (c Config) Validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("config: PORT must be 1-65535, got %d", c.Port)
	}

	switch c.Payment.Provider {
	case "cashfree":
		if c.Payment.Cashfree.ClientID == "" || c.Payment.Cashfree.ClientSecret == "" {
			return fmt.Errorf("config: cashfree selected but CASHFREE_CLIENT_ID / CASHFREE_CLIENT_SECRET missing")
		}
		if c.Payment.Cashfree.Environment != "SANDBOX" && c.Payment.Cashfree.Environment != "PRODUCTION" {
			return fmt.Errorf("config: CASHFREE_ENV must be SANDBOX or PRODUCTION, got %q", c.Payment.Cashfree.Environment)
		}
	case "":
		return fmt.Errorf("config: PAYMENT_PROVIDER is required")
	default:
		return fmt.Errorf("config: unknown PAYMENT_PROVIDER %q", c.Payment.Provider)
	}
	return nil
}

// --- env helpers ---

func getIntEnv(key string, fallback int) int {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	i, err := strconv.Atoi(val)
	if err != nil {
		return fallback
	}
	return i
}

func getSliceEnv(key string) []string {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// loadDotEnv performs a minimal .env parse: KEY=VALUE lines, ignoring blanks
// and # comments. Existing environment variables take precedence.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // no .env is fine; rely on real env vars
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
