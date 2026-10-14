package jobs

import (
	"context"
	"log/slog"
	"time"
)

// OrderReconciler defines the interface for order reconciliation.
type OrderReconciler interface {
	ReconcilePendingOrders(ctx context.Context, olderThan time.Duration) (int, error)
}

// ReconciliationJob runs periodic reconciliation of pending orders.
type ReconciliationJob struct {
	reconciler OrderReconciler
	interval   time.Duration
	olderThan  time.Duration
	stopCh     chan struct{}
}

// NewReconciliationJob creates a new reconciliation job.
// interval: how often to run (e.g., 5 minutes)
// olderThan: only reconcile orders older than this (e.g., 10 minutes)
func NewReconciliationJob(reconciler OrderReconciler, interval, olderThan time.Duration) *ReconciliationJob {
	return &ReconciliationJob{
		reconciler: reconciler,
		interval:   interval,
		olderThan:  olderThan,
		stopCh:     make(chan struct{}),
	}
}

// Start begins the reconciliation loop in a goroutine.
func (j *ReconciliationJob) Start(ctx context.Context) {
	go func() {
		slog.Info("reconciliation job started", "interval", j.interval, "older_than", j.olderThan)

		// Run immediately on startup
		j.run(ctx)

		ticker := time.NewTicker(j.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				j.run(ctx)
			case <-j.stopCh:
				slog.Info("reconciliation job stopped")
				return
			case <-ctx.Done():
				slog.Info("reconciliation job context cancelled")
				return
			}
		}
	}()
}

// Stop signals the reconciliation job to stop.
func (j *ReconciliationJob) Stop() {
	close(j.stopCh)
}

func (j *ReconciliationJob) run(ctx context.Context) {
	slog.Debug("running reconciliation")

	count, err := j.reconciler.ReconcilePendingOrders(ctx, j.olderThan)
	if err != nil {
		slog.Error("reconciliation failed", "err", err)
		return
	}

	if count > 0 {
		slog.Info("reconciliation completed", "reconciled", count)
	}
}
