package config

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config holds core application configuration.
type Config struct {
	Env            string   `env:"APP_ENV" envDefault:"development"`
	DatabaseURL    string   `env:"DATABASE_URL,required"`
	Port           int      `env:"PORT" envDefault:"8081"`
	GRPCPort       int      `env:"GRPC_PORT" envDefault:"50051"`
	ServiceToken   string   `env:"SERVICE_TOKEN,required"`
	Gateway        string   `env:"PAYMENT_PROVIDER" envDefault:"cashfree"`
	NodeWebhookURL string   `env:"NODE_WEBHOOK_URL,required"`
	CorsOrigins    []string `env:"CORS_ORIGINS" envSeparator:","`

	// Grafana Cloud (traces, metrics, logs via OTLP)
	TempoEndpoint string `env:"TEMPO_ENDPOINT"`
	TempoUser     string `env:"TEMPO_USER"`
	TempoAPIKey   string `env:"TEMPO_API_KEY"`

	// SSM config (only used when APP_ENV=production)
	SSMPrefix string `env:"SSM_PREFIX" envDefault:"/ky27/payment/prod"` // e.g., /ky27/payment/prod
	AWSRegion string `env:"AWS_REGION" envDefault:"ap-south-1"`
}

func (c Config) IsProd() bool    { return c.Env == "production" }
func (c Config) IsStaging() bool { return c.Env == "staging" }
func (c Config) IsDev() bool     { return c.Env == "development" }

// Load reads config from .env file or SSM based on APP_ENV.
// APP_ENV itself must be set via environment variable.
func Load() (Config, error) {
	// APP_ENV determines source — must come from env, not SSM (chicken/egg)
	appEnv := os.Getenv("APP_ENV")
	if appEnv == "" {
		appEnv = "development"
	}

	if appEnv == "production" {
		return loadFromSSM()
	}
	return loadFromEnvFile()
}

// loadFromEnvFile loads config from .env file (development, test, staging).
func loadFromEnvFile() (Config, error) {
	_ = godotenv.Load() // ignore error if .env missing

	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// loadFromSSM fetches all parameters under SSM_PREFIX and sets them as env vars.
func loadFromSSM() (Config, error) {
	// First load minimal config to get SSM_PREFIX and AWS_REGION
	_ = godotenv.Load()
	prefix := os.Getenv("SSM_PREFIX")
	if prefix == "" {
		prefix = "/ky27/payment/prod"
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "ap-south-1"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Load AWS config (uses IAM role in ECS/EC2, or ~/.aws/credentials locally)
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return Config{}, fmt.Errorf("config: aws: %w", err)
	}

	client := ssm.NewFromConfig(awsCfg)

	// Fetch all parameters under prefix
	params, err := fetchSSMParameters(ctx, client, prefix)
	if err != nil {
		return Config{}, fmt.Errorf("config: ssm: %w", err)
	}

	// Set as environment variables for env.Parse to pick up
	for key, value := range params {
		os.Setenv(key, value)
	}

	// Now parse using same struct
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// fetchSSMParameters gets all parameters under a path prefix.
// Parameter names like /ky27/prod/database_url become DATABASE_URL.
func fetchSSMParameters(ctx context.Context, client *ssm.Client, prefix string) (map[string]string, error) {
	params := make(map[string]string)

	paginator := ssm.NewGetParametersByPathPaginator(client, &ssm.GetParametersByPathInput{
		Path:           aws.String(prefix),
		WithDecryption: aws.Bool(true), // Decrypt SecureString params
		Recursive:      aws.Bool(true),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, param := range page.Parameters {
			// /ky27/prod/database_url → DATABASE_URL
			name := *param.Name
			name = strings.TrimPrefix(name, prefix+"/")
			name = strings.ToUpper(name)
			params[name] = *param.Value
		}
	}

	return params, nil
}
