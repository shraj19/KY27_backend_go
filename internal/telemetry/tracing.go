package telemetry

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"

	"ky27/backend/internal/config"
)

const serviceName = "ky27-payment"

// Init sets up OTel exporters for traces, metrics, and logs to Grafana Cloud.
func Init(ctx context.Context, cfg config.Config) (shutdown func(context.Context) error, err error) {
	if cfg.TempoEndpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	// Resource identifies this service in Grafana Cloud.
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.DeploymentEnvironment(cfg.Env),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	// Auth header for Grafana Cloud OTLP endpoint.
	auth := map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.TempoUser+":"+cfg.TempoAPIKey)),
	}

	tp, err := initTracer(ctx, cfg.TempoEndpoint, auth, res)
	if err != nil {
		return nil, err
	}

	mp, err := initMeter(ctx, cfg.TempoEndpoint, auth, res)
	if err != nil {
		return nil, err
	}

	lp, err := initLogger(ctx, cfg.TempoEndpoint, auth, res)
	if err != nil {
		return nil, err
	}

	// Shutdown flushes pending data and closes connections.
	return func(ctx context.Context) error {
		_ = tp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		_ = lp.Shutdown(ctx)
		return nil
	}, nil
}

// initTracer configures trace export to Tempo.
func initTracer(ctx context.Context, endpoint string, auth map[string]string, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithURLPath("/otlp/v1/traces"),
		otlptracehttp.WithHeaders(auth),
	)
	if err != nil {
		return nil, fmt.Errorf("trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// Propagator enables trace context across service boundaries.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp, nil
}

// initMeter configures metric export to Prometheus/Mimir.
func initMeter(ctx context.Context, endpoint string, auth map[string]string, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	exp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(endpoint),
		otlpmetrichttp.WithURLPath("/otlp/v1/metrics"),
		otlpmetrichttp.WithHeaders(auth),
	)
	if err != nil {
		return nil, fmt.Errorf("metric exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(15*time.Second))),
	)
	otel.SetMeterProvider(mp)
	return mp, nil
}

// initLogger configures log export to Loki and bridges slog to OTel.
func initLogger(ctx context.Context, endpoint string, auth map[string]string, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	exp, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpoint(endpoint),
		otlploghttp.WithURLPath("/otlp/v1/logs"),
		otlploghttp.WithHeaders(auth),
	)
	if err != nil {
		return nil, fmt.Errorf("log exporter: %w", err)
	}

	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
	)
	otel.SetLoggerProvider(lp)

	// Bridge slog → OTel so slog.Info() sends to Grafana Cloud.
	slog.SetDefault(slog.New(otelslog.NewHandler(serviceName)))
	return lp, nil
}
