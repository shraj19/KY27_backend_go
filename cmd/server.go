package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth"

	pb "ky27/backend/gen/payment/v1"
	"ky27/backend/internal/config"
	"ky27/backend/internal/db"
	grpcserver "ky27/backend/internal/grpc"
	"ky27/backend/internal/jobs"
	"ky27/backend/internal/logger"
	"ky27/backend/internal/metrics"
	"ky27/backend/internal/middleware"
	"ky27/backend/internal/order"
	"ky27/backend/internal/payment"
	"ky27/backend/internal/telemetry"
	"ky27/backend/internal/webhook"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	logger.Init(cfg)

	// Init tracing and metrics
	shutdownTracer, err := telemetry.Init(ctx, cfg)
	if err != nil {
		slog.Error("tracing init failed", "err", err)
		os.Exit(1)
	}

	if err := metrics.Init(); err != nil {
		slog.Error("metrics init failed", "err", err)
		os.Exit(1)
	}

	gateway, err := newGateway(cfg.Gateway)
	if err != nil {
		slog.Error("gateway init failed", "err", err)
		os.Exit(1)
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "err", err)
		os.Exit(1)
	}

	directURL := strings.Replace(cfg.DatabaseURL, "-pooler", "", 1)
	riverPool, err := db.ConnectDirect(ctx, directURL)
	if err != nil {
		slog.Error("db connect (river) failed", "err", err)
		os.Exit(1)
	}

	riverClient, err := setupRiver(ctx, riverPool, cfg.NodeWebhookURL, cfg.ServiceToken)
	if err != nil {
		slog.Error("river init failed", "err", err)
		os.Exit(1)
	}

	// order.Service owns the full order lifecycle
	queries := db.New(pool)
	jobAdapter := &riverJobAdapter{client: riverClient}
	orderSvc := order.NewService(gateway, riverPool, queries, jobAdapter, cfg.Gateway)

	grpcAddr := fmt.Sprintf(":%d", cfg.GRPCPort)
	grpcSrv := startGRPC(grpcAddr, orderSvc, cfg.ServiceToken)

	httpAddr := fmt.Sprintf(":%d", cfg.Port)
	httpSrv := startHTTP(httpAddr, cfg, gateway, orderSvc)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown failed", "err", err)
	}

	grpcSrv.GracefulStop()

	if err := riverClient.Stop(shutdownCtx); err != nil {
		slog.Error("river shutdown failed", "err", err)
	}

	if err := shutdownTracer(shutdownCtx); err != nil {
		slog.Error("tracer shutdown failed", "err", err)
	}

	pool.Close()
	riverPool.Close()

	slog.Info("shutdown complete")
}

func setupRiver(ctx context.Context, pool *pgxpool.Pool, nodeWebhookURL, serviceToken string) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewNotifyNodeWorker(nodeWebhookURL, serviceToken))

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	if err := riverClient.Start(ctx); err != nil {
		return nil, fmt.Errorf("start client: %w", err)
	}

	slog.Info("river started")
	return riverClient, nil
}

func startGRPC(addr string, orderSvc *order.Service, token string) *grpc.Server {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("grpc listen failed", "err", err, "addr", addr)
		os.Exit(1)
	}

	creds, err := credentials.NewServerTLSFromFile("certs/server.crt", "certs/server.key")
	if err != nil {
		slog.Error("grpc tls load failed", "err", err)
		os.Exit(1)
	}

	srv := grpc.NewServer(
		grpc.Creds(creds),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			auth.UnaryServerInterceptor(middleware.AuthInterceptor(token)),
		),
	)
	pb.RegisterPaymentServiceServer(srv, grpcserver.NewPaymentServer(orderSvc))
	reflection.Register(srv)

	go func() {
		slog.Info("grpc listening", "addr", addr)
		if err := srv.Serve(lis); err != nil {
			slog.Error("grpc serve failed", "err", err)
		}
	}()

	return srv
}

func startHTTP(addr string, cfg config.Config, gw payment.PaymentGateway, orderSvc *order.Service) *http.Server {
	r := gin.Default()

	if mw := middleware.CORS(cfg.CorsOrigins); mw != nil {
		r.Use(mw)
	}

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "provider": cfg.Gateway})
	})

	webhookHandler := webhook.NewHandler(orderSvc)
	webhookHandler.RegisterRoutes(r, map[string]payment.PaymentGateway{
		cfg.Gateway: gw,
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	go func() {
		slog.Info("http listening", "addr", addr, "provider", cfg.Gateway)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http serve failed", "err", err)
		}
	}()

	return srv
}

// riverJobAdapter bridges River client to order.JobInserter interface.
type riverJobAdapter struct {
	client *river.Client[pgx.Tx]
}

func (r *riverJobAdapter) InsertTx(ctx context.Context, tx pgx.Tx, orderID, status string, amountPaise int64, paidAt string) error {
	_, err := r.client.InsertTx(ctx, tx, jobs.NotifyNodeArgs{
		OrderID:     orderID,
		Status:      status,
		AmountPaise: amountPaise,
		PaidAt:      paidAt,
	}, nil)
	return err
}

func newGateway(provider string) (payment.PaymentGateway, error) {
	switch provider {
	case "cashfree":
		cfg, err := config.LoadCashfreeConfig()
		if err != nil {
			return nil, err
		}
		return payment.NewCashfreeProvider(cfg)
	default:
		return nil, fmt.Errorf("unknown payment provider: %s", provider)
	}
}
