package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/riverqueue/river"

	"ky27/backend/internal/metrics"
)

// -----------------------------------------------------------------------------
// Job Arguments
// -----------------------------------------------------------------------------

// NotifyNodeArgs contains the data sent to Node when a payment completes.
type NotifyNodeArgs struct {
	OrderID     string `json:"order_id"`
	Status      string `json:"status"`
	AmountPaise int64  `json:"amount_paise"`
	PaidAt      string `json:"paid_at"`
}

func (NotifyNodeArgs) Kind() string { return "notify_node" }

// -----------------------------------------------------------------------------
// Worker
// -----------------------------------------------------------------------------

// NotifyNodeWorker sends payment completion webhooks to the Node backend.
type NotifyNodeWorker struct {
	river.WorkerDefaults[NotifyNodeArgs]
	nodeWebhookURL string
	serviceToken   string
	httpClient     *http.Client
}

// NewNotifyNodeWorker creates a worker that posts to the given Node URL.
// The serviceToken is sent as "Authorization: Bearer <token>" for auth.
func NewNotifyNodeWorker(nodeWebhookURL, serviceToken string) *NotifyNodeWorker {
	return &NotifyNodeWorker{
		nodeWebhookURL: nodeWebhookURL,
		serviceToken:   serviceToken,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (w *NotifyNodeWorker) Work(ctx context.Context, job *river.Job[NotifyNodeArgs]) error {
	payload, err := json.Marshal(job.Args)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.nodeWebhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.serviceToken)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		metrics.RecordNodeNotification(ctx, false)
		slog.WarnContext(ctx, "node notify failed", "err", err, "order_id", job.Args.OrderID, "attempt", job.Attempt)
		return fmt.Errorf("post to node: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		metrics.RecordNodeNotification(ctx, true)
		slog.InfoContext(ctx, "node notified", "order_id", job.Args.OrderID, "status_code", resp.StatusCode)
		return nil
	}

	metrics.RecordNodeNotification(ctx, false)
	slog.WarnContext(ctx, "node returned error", "order_id", job.Args.OrderID, "status_code", resp.StatusCode, "attempt", job.Attempt)
	return fmt.Errorf("node returned status %d", resp.StatusCode)
}
