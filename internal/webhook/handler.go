package webhook

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"ky27/backend/internal/metrics"
	"ky27/backend/internal/order"
	"ky27/backend/internal/payment"
)

// Handler processes payment webhooks — thin HTTP adapter.
type Handler struct {
	orderSvc *order.Service
}

func NewHandler(orderSvc *order.Service) *Handler {
	return &Handler{orderSvc: orderSvc}
}

func (h *Handler) RegisterRoutes(r *gin.Engine, gateways map[string]payment.PaymentGateway) {
	for name, gw := range gateways {
		r.POST("/webhooks/"+name, h.handleWebhook(gw))
	}
}

func (h *Handler) handleWebhook(gw payment.PaymentGateway) gin.HandlerFunc {
	headers := gw.WebhookHeaders()

	return func(c *gin.Context) {
		ctx := c.Request.Context()

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
			return
		}

		signature := c.GetHeader(headers.Signature)
		if signature == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing signature header"})
			return
		}

		timestamp := ""
		if headers.Timestamp != "" {
			timestamp = c.GetHeader(headers.Timestamp)
			if timestamp == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "missing timestamp header"})
				return
			}
		}

		event, err := gw.VerifyWebhook(signature, body, timestamp)
		if err != nil {
			metrics.RecordWebhook(ctx, false)
			slog.WarnContext(ctx, "webhook verification failed", "err", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		metrics.RecordWebhook(ctx, true)

		// Dispatch to order service based on status
		if err := h.processEvent(ctx, event); err != nil {
			slog.ErrorContext(ctx, "webhook processing failed", "err", err, "order_id", event.OrderID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

func (h *Handler) processEvent(ctx context.Context, event payment.WebhookEvent) error {
	switch event.Status {
	case payment.StatusPaid:
		return h.orderSvc.Complete(ctx, order.CompleteRequest{
			OrderID:           event.OrderID,
			AmountPaise:       event.AmountPaise,
			PaidAt:            event.PaidAt,
			ProviderPaymentID: event.ProviderPaymentID,
		})

	case payment.StatusFailed:
		return h.orderSvc.Fail(ctx, event.OrderID, "payment_failed")

	default:
		slog.DebugContext(ctx, "webhook ignored", "status", event.Status, "order_id", event.OrderID)
		return nil
	}
}
