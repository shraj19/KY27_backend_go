package config

import (
	"fmt"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config is the fully-loaded, validated application configuration.
type Config struct {
	Port        int      `envconfig:"PORT" default:"8081"`
	CorsOrigins []string `envconfig:"CORS_ORIGINS"`
	DatabaseURL string   `envconfig:"DATABASE_URL" required:"true"`
	Gateway     string   `envconfig:"PAYMENT_PROVIDER" required:"true"`
}

// Load reads .env (if present), then populates Config from environment.
// Fails fast if required config is missing.
func Load() (Config, error) {
	_ = godotenv.Load() // .env → os.Environ; no error if file missing

	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}

	return cfg, nil
}
