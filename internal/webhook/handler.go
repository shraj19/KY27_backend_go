package webhook

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"ky27/backend/internal/db"
	"ky27/backend/internal/jobs"
	"ky27/backend/internal/metrics"
	"ky27/backend/internal/payment"
)

// TxBeginner starts a transaction. Implemented by *pgxpool.Pool.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// JobInserter inserts jobs transactionally. Implemented by *river.Client.
type JobInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args jobs.NotifyNodeArgs, opts any) error
}

// Handler processes payment webhooks.
type Handler struct {
	pool   TxBeginner
	jobs   JobInserter
}

func NewHandler(pool TxBeginner, jobs JobInserter) *Handler {
	return &Handler{pool: pool, jobs: jobs}
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
			metrics.RecordWebhook(c.Request.Context(), false)
			slog.WarnContext(c.Request.Context(), "webhook signature verification failed", "err", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		metrics.RecordWebhook(c.Request.Context(), true)

		if err := h.processPaymentEvent(c.Request.Context(), event); err != nil {
			slog.ErrorContext(c.Request.Context(), "webhook processing failed", "err", err, "order_id", event.OrderID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

func (h *Handler) processPaymentEvent(ctx context.Context, event payment.WebhookEvent) error {
	if event.Status != payment.StatusPaid {
		slog.DebugContext(ctx, "webhook ignored", "status", event.Status, "order_id", event.OrderID)
		return nil
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "webhook tx begin failed", "err", err)
		return err
	}
	defer tx.Rollback(ctx)

	queries := db.New(tx)
	paidAt := pgtype.Timestamptz{Time: event.PaidAt, Valid: !event.PaidAt.IsZero()}

	// Idempotent transition: only succeeds if ACTIVE + amount matches
	_, err = queries.MarkOrderPaidIfActive(ctx, db.MarkOrderPaidIfActiveParams{
		ID:         event.OrderID,
		PaidAt:     paidAt,
		TotalPaise: event.AmountPaise,
	})

	if errors.Is(err, pgx.ErrNoRows) {
		return h.handleNoTransition(ctx, queries, event)
	}
	if err != nil {
		slog.ErrorContext(ctx, "mark order paid failed", "err", err, "order_id", event.OrderID)
		return err
	}

	// Transition succeeded — enqueue notification
	err = h.jobs.InsertTx(ctx, tx, jobs.NotifyNodeArgs{
		OrderID:     event.OrderID,
		Status:      string(event.Status),
		AmountPaise: event.AmountPaise,
		PaidAt:      event.PaidAt.Format(time.RFC3339),
	}, nil)
	if err != nil {
		slog.ErrorContext(ctx, "river insert failed", "err", err, "order_id", event.OrderID)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "webhook tx commit failed", "err", err, "order_id", event.OrderID)
		return err
	}

	slog.InfoContext(ctx, "order paid",
		"order_id", event.OrderID,
		"amount_paise", event.AmountPaise,
		"provider_payment_id", event.ProviderPaymentID)
	metrics.RecordPaymentSuccess(ctx, event.AmountPaise)
	return nil
}

func (h *Handler) handleNoTransition(ctx context.Context, queries db.Querier, event payment.WebhookEvent) error {
	order, err := queries.GetOrderStatus(ctx, event.OrderID)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.WarnContext(ctx, "webhook for unknown order",
			"order_id", event.OrderID,
			"provider_payment_id", event.ProviderPaymentID)
		return nil
	}
	if err != nil {
		return err
	}

	if order.Status == "PAID" {
		slog.DebugContext(ctx, "duplicate webhook ignored", "order_id", event.OrderID)
		return nil
	}

	if order.TotalPaise != event.AmountPaise {
		slog.ErrorContext(ctx, "webhook amount mismatch",
			"order_id", event.OrderID,
			"expected_paise", order.TotalPaise,
			"webhook_paise", event.AmountPaise,
			"provider_payment_id", event.ProviderPaymentID)
		metrics.RecordPaymentFailed(ctx, "amount_mismatch")
		return nil
	}

	slog.WarnContext(ctx, "webhook for non-active order",
		"order_id", event.OrderID,
		"status", order.Status,
		"provider_payment_id", event.ProviderPaymentID)
	return nil
}
