package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config holds core application configuration.
// Provider-specific config is loaded separately based on Gateway.
type Config struct {
	Env            string   `env:"APP_ENV" envDefault:"development"` // development | staging | production
	DatabaseURL    string   `env:"DATABASE_URL,required"`
	Port           int      `env:"PORT" envDefault:"8081"`
	GRPCPort       int      `env:"GRPC_PORT" envDefault:"50051"`
	ServiceToken   string   `env:"SERVICE_TOKEN,required"`
	Gateway        string   `env:"PAYMENT_PROVIDER" envDefault:"cashfree"`
	NodeWebhookURL string   `env:"NODE_WEBHOOK_URL,required"`
	CorsOrigins    []string `env:"CORS_ORIGINS" envSeparator:","`
}

func (c Config) IsProd() bool    { return c.Env == "production" }
func (c Config) IsStaging() bool { return c.Env == "staging" }
func (c Config) IsDev() bool     { return c.Env == "development" }

// Load reads .env (if present), then populates Config from environment.
func Load() (Config, error) {
	_ = godotenv.Load() // ignore error if .env missing

	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
