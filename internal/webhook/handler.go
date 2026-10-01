package webhook

import (
	"context"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"ky27/backend/internal/db"
	"ky27/backend/internal/jobs"
	"ky27/backend/internal/payment"
)

// Handler handles payment gateway webhooks.
type Handler struct {
	pool        *pgxpool.Pool // for regular queries (pooled connection)
	riverPool   *pgxpool.Pool // for River transactions (direct connection)
	riverClient *river.Client[pgx.Tx]
}

// NewHandler creates a webhook handler.
func NewHandler(pool, riverPool *pgxpool.Pool, rc *river.Client[pgx.Tx]) *Handler {
	return &Handler{
		pool:        pool,
		riverPool:   riverPool,
		riverClient: rc,
	}
}

// RegisterRoutes registers webhook routes for each gateway.
// Each provider gets its own endpoint since gateways POST to different URLs.
func (h *Handler) RegisterRoutes(r *gin.Engine, gateways map[string]payment.PaymentGateway) {
	for name, gw := range gateways {
		r.POST("/webhooks/"+name, h.handleWebhook(gw))
	}
}

// handleWebhook returns a generic webhook handler for any gateway.
// The gateway tells us which headers to read via WebhookHeaders().
func (h *Handler) handleWebhook(gw payment.PaymentGateway) gin.HandlerFunc {
	headers := gw.WebhookHeaders()

	return func(c *gin.Context) {
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
}

func (h *Handler) processPaymentEvent(ctx context.Context, event payment.WebhookEvent) error {
	if event.Status != payment.StatusPaid {
		log.Printf("webhook: ignoring event with status %s for order %s", event.Status, event.OrderID)
		return nil
	}

	// Use transaction for atomic DB update + job enqueue
	// Must use riverPool (direct connection) for River's InsertTx
	tx, err := h.riverPool.Begin(ctx)
	if err != nil {
		log.Printf("webhook: begin tx failed: %v", err)
		return err
	}
	defer tx.Rollback(ctx)

	queries := db.New(tx)
	if _, err := queries.MarkOrderPaid(ctx, db.MarkOrderPaidParams{
		ID:              event.OrderID,
		ProviderOrderID: nil,
	}); err != nil {
		log.Printf("webhook: mark order paid failed: %v", err)
		return err
	}

	_, err = h.riverClient.InsertTx(ctx, tx, jobs.NotifyNodeArgs{
		OrderID:     event.OrderID,
		Status:      string(event.Status),
		AmountPaise: event.AmountPaise,
		PaidAt:      event.PaidAt.Format(time.RFC3339),
	}, nil)
	if err != nil {
		log.Printf("webhook: insert job failed: %v", err)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("webhook: commit failed: %v", err)
		return err
	}

	log.Printf("webhook: order %s marked paid, notification enqueued", event.OrderID)
	return nil
}
