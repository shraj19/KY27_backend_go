//go:build integration

package jobs_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"ky27/backend/internal/jobs"
)

func TestRiverInsert(t *testing.T) {
	godotenv.Load("../../.env")

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Fatal("DATABASE_URL not set")
	}

	// Use direct connection (remove -pooler)
	directURL := strings.Replace(dbURL, "-pooler", "", 1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, directURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Log("Connected to database")

	// Create River client (insert-only, no Start)
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewNotifyNodeWorker("http://localhost:3000/webhook", "test-token"))

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Workers: workers,
	})
	if err != nil {
		t.Fatalf("create river client: %v", err)
	}
	t.Log("River client created")

	// Test 1: Simple insert (no transaction)
	t.Run("Insert", func(t *testing.T) {
		args := jobs.NotifyNodeArgs{
			OrderID:     "TEST-ORDER-001",
			Status:      "paid",
			AmountPaise: 10000,
			PaidAt:      time.Now().Format(time.RFC3339),
		}

		result, err := client.Insert(ctx, args, nil)
		if err != nil {
			t.Fatalf("insert failed: %v", err)
		}
		t.Logf("Inserted job ID: %d, State: %s", result.Job.ID, result.Job.State)
	})

	// Test 2: Insert within transaction
	t.Run("InsertTx", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer tx.Rollback(ctx)

		args := jobs.NotifyNodeArgs{
			OrderID:     "TEST-ORDER-002",
			Status:      "paid",
			AmountPaise: 20000,
			PaidAt:      time.Now().Format(time.RFC3339),
		}

		result, err := client.InsertTx(ctx, tx, args, nil)
		if err != nil {
			t.Fatalf("insertTx failed: %v", err)
		}
		t.Logf("InsertTx job ID: %d, State: %s", result.Job.ID, result.Job.State)

		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		t.Log("Transaction committed")
	})
}
