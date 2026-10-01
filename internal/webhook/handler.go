package webhook

import (
	"context"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"ky27/backend/internal/db"
	"ky27/backend/internal/jobs"
	"ky27/backend/internal/payment"
)

// Handler handles payment gateway webhooks.
type Handler struct {
	gateway     payment.PaymentGateway
	queries     *db.Queries
	riverClient *river.Client[pgx.Tx]
}

// NewHandler creates a webhook handler.
func NewHandler(gw payment.PaymentGateway, q *db.Queries, rc *river.Client[pgx.Tx]) *Handler {
	return &Handler{
		gateway:     gw,
		queries:     q,
		riverClient: rc,
	}
}

// RegisterRoutes registers webhook routes on the Gin router.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	r.POST("/webhooks/cashfree", h.HandleCashfree)
}

// HandleCashfree processes Cashfree payment webhooks.
func (h *Handler) HandleCashfree(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	signature := c.GetHeader("x-webhook-signature")
	timestamp := c.GetHeader("x-webhook-timestamp")

	if signature == "" || timestamp == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing signature headers"})
		return
	}

	event, err := h.gateway.VerifyWebhook(signature, body, timestamp)
	if err != nil {
		log.Printf("webhook: signature verification failed: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	if err := h.processPaymentEvent(c.Request.Context(), event); err != nil {
		log.Printf("webhook: process event failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) processPaymentEvent(ctx context.Context, event payment.WebhookEvent) error {
	if event.Status != payment.StatusPaid {
		log.Printf("webhook: ignoring event with status %s for order %s", event.Status, event.OrderID)
		return nil
	}

	// Update order status
	if _, err := h.queries.MarkOrderPaid(ctx, db.MarkOrderPaidParams{
		ID:              event.OrderID,
		ProviderOrderID: nil,
	}); err != nil {
		return err
	}

	// Enqueue notification to Node
	_, err := h.riverClient.Insert(ctx, jobs.NotifyNodeArgs{
		OrderID:     event.OrderID,
		Status:      string(event.Status),
		AmountPaise: event.AmountPaise,
		PaidAt:      event.PaidAt.Format(time.RFC3339),
	}, nil)
	if err != nil {
		return err
	}

	log.Printf("webhook: order %s marked paid, notification enqueued", event.OrderID)
	return nil
}
