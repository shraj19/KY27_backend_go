package webhook

import (
	"context"
	"io"
	"log/slog"
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

type Handler struct {
	pool        *pgxpool.Pool
	riverPool   *pgxpool.Pool
	riverClient *river.Client[pgx.Tx]
}

func NewHandler(pool, riverPool *pgxpool.Pool, rc *river.Client[pgx.Tx]) *Handler {
	return &Handler{
		pool:        pool,
		riverPool:   riverPool,
		riverClient: rc,
	}
}

func (h *Handler) RegisterRoutes(r *gin.Engine, gateways map[string]payment.PaymentGateway) {
	for name, gw := range gateways {
		r.POST("/webhooks/"+name, h.handleWebhook(gw))
	}
}

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
			slog.Warn("webhook signature verification failed", "err", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		if err := h.processPaymentEvent(c.Request.Context(), event); err != nil {
			slog.Error("webhook processing failed", "err", err, "order_id", event.OrderID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

func (h *Handler) processPaymentEvent(ctx context.Context, event payment.WebhookEvent) error {
	if event.Status != payment.StatusPaid {
		slog.Debug("webhook ignored", "status", event.Status, "order_id", event.OrderID)
		return nil
	}

	tx, err := h.riverPool.Begin(ctx)
	if err != nil {
		slog.Error("webhook tx begin failed", "err", err)
		return err
	}
	defer tx.Rollback(ctx)

	queries := db.New(tx)
	if _, err := queries.MarkOrderPaid(ctx, db.MarkOrderPaidParams{
		ID:              event.OrderID,
		ProviderOrderID: nil,
	}); err != nil {
		slog.Error("mark order paid failed", "err", err, "order_id", event.OrderID)
		return err
	}

	_, err = h.riverClient.InsertTx(ctx, tx, jobs.NotifyNodeArgs{
		OrderID:     event.OrderID,
		Status:      string(event.Status),
		AmountPaise: event.AmountPaise,
		PaidAt:      event.PaidAt.Format(time.RFC3339),
	}, nil)
	if err != nil {
		slog.Error("river insert failed", "err", err, "order_id", event.OrderID)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("webhook tx commit failed", "err", err, "order_id", event.OrderID)
		return err
	}

	slog.Info("order paid", "order_id", event.OrderID, "amount_paise", event.AmountPaise)
	return nil
}
