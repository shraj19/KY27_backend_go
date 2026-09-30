package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config is the fully-loaded, validated application configuration.
type Config struct {
	Port        int      `envconfig:"PORT" default:"8081"`
	CorsOrigins []string `envconfig:"CORS_ORIGINS"`
	DatabaseURL string   `envconfig:"DATABASE_URL" required:"true"`
	Gateway     string   `envconfig:"PAYMENT_PROVIDER" required:"true"`
	GatewayCfg  map[string]string
}

// Load reads .env (if present), then populates Config from environment.
// Fails fast if required config is missing.
func Load() (Config, error) {
	_ = godotenv.Load() // .env → os.Environ; no error if file missing

	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}

	cfg.GatewayCfg = loadProviderEnv(cfg.Gateway)
	return cfg, nil
}

// loadProviderEnv loads all env vars matching the provider's prefix.
// For "cashfree", loads CASHFREE_* into a map keyed by full env var name.
func loadProviderEnv(provider string) map[string]string {
	if provider == "" {
		return nil
	}

	prefix := strings.ToUpper(provider) + "_"
	cfg := make(map[string]string)

	for _, kv := range os.Environ() {
		key, val, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(key, prefix) {
			cfg[key] = val
		}
	}
	return cfg
}
