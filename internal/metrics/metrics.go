package metrics

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter = otel.Meter("ky27-payment")

	PaymentsTotal      metric.Int64Counter
	PaymentAmountTotal metric.Int64Counter
	GatewayRequests    metric.Int64Counter
	GatewayLatency     metric.Float64Histogram
	WebhooksReceived   metric.Int64Counter
	NodeNotifications  metric.Int64Counter
)

func Init() error {
	var err error

	PaymentsTotal, err = meter.Int64Counter("payments_total",
		metric.WithDescription("Total payment attempts"),
	)
	if err != nil {
		return err
	}

	PaymentAmountTotal, err = meter.Int64Counter("payment_amount_paise_total",
		metric.WithDescription("Total payment amount in paise"),
	)
	if err != nil {
		return err
	}

	GatewayRequests, err = meter.Int64Counter("gateway_requests_total",
		metric.WithDescription("Payment gateway API requests"),
	)
	if err != nil {
		return err
	}

	GatewayLatency, err = meter.Float64Histogram("gateway_latency_seconds",
		metric.WithDescription("Payment gateway API latency"),
	)
	if err != nil {
		return err
	}

	WebhooksReceived, err = meter.Int64Counter("webhooks_received_total",
		metric.WithDescription("Webhooks received from payment gateways"),
	)
	if err != nil {
		return err
	}

	NodeNotifications, err = meter.Int64Counter("node_notifications_total",
		metric.WithDescription("Payment notifications sent to Node backend"),
	)
	if err != nil {
		return err
	}

	return nil
}

func RecordPaymentSuccess(ctx context.Context, amountPaise int64) {
	PaymentsTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "success")))
	PaymentAmountTotal.Add(ctx, amountPaise, metric.WithAttributes(attribute.String("status", "success")))
}

func RecordPaymentFailed(ctx context.Context, reason string) {
	PaymentsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("status", "failed"),
			attribute.String("reason", reason),
		),
	)
}

func RecordPaymentExpired(ctx context.Context) {
	PaymentsTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "expired")))
}

func RecordGatewayRequest(ctx context.Context, gateway string, success bool, latencySeconds float64) {
	status := "success"
	if !success {
		status = "error"
	}
	attrs := metric.WithAttributes(
		attribute.String("gateway", gateway),
		attribute.String("status", status),
	)
	GatewayRequests.Add(ctx, 1, attrs)
	GatewayLatency.Record(ctx, latencySeconds, metric.WithAttributes(attribute.String("gateway", gateway)))
}

func RecordWebhook(ctx context.Context, valid bool) {
	status := "valid"
	if !valid {
		status = "invalid"
	}
	WebhooksReceived.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}

func RecordNodeNotification(ctx context.Context, success bool) {
	status := "success"
	if !success {
		status = "failed"
	}
	NodeNotifications.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}
