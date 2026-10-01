package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/riverqueue/river"
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
		return fmt.Errorf("post to node: %w", err) // River will retry
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil // Success
	}

	// Non-2xx = retry
	return fmt.Errorf("node returned status %d", resp.StatusCode)
}
