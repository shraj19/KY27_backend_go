package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth"

	pb "ky27/backend/gen/payment/v1"
	"ky27/backend/internal/config"
	"ky27/backend/internal/db"
	grpcserver "ky27/backend/internal/grpc"
	"ky27/backend/internal/jobs"
	"ky27/backend/internal/middleware"
	"ky27/backend/internal/order"
	"ky27/backend/internal/payment"
	"ky27/backend/internal/webhook"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	gateway, err := newGateway(cfg.Gateway)
	if err != nil {
		log.Fatalf("gateway: %v", err)
	}

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	queries := db.New(pool)
	orderSvc := order.NewService(gateway, queries, cfg.Gateway)

	riverClient, err := setupRiver(context.Background(), pool, cfg.NodeWebhookURL, cfg.ServiceToken)
	if err != nil {
		log.Fatalf("river: %v", err)
	}

	// Start servers
	grpcAddr := fmt.Sprintf(":%d", cfg.GRPCPort)
	grpcSrv := startGRPC(grpcAddr, orderSvc, cfg.ServiceToken)

	httpAddr := fmt.Sprintf(":%d", cfg.Port)
	httpSrv := startHTTP(httpAddr, cfg, gateway, queries, riverClient)

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Stop HTTP server
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	// Stop gRPC server
	grpcSrv.GracefulStop()

	// Stop River workers
	if err := riverClient.Stop(ctx); err != nil {
		log.Printf("river shutdown: %v", err)
	}

	// Close database
	pool.Close()

	log.Println("shutdown complete")
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

	log.Println("river: job queue started")
	return riverClient, nil
}

func startGRPC(addr string, orderSvc *order.Service, token string) *grpc.Server {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("grpc: listen: %v", err)
	}

	creds, err := credentials.NewServerTLSFromFile("certs/server.crt", "certs/server.key")
	if err != nil {
		log.Fatalf("grpc: load TLS certs: %v", err)
	}

	srv := grpc.NewServer(
		grpc.Creds(creds),
		grpc.UnaryInterceptor(auth.UnaryServerInterceptor(middleware.AuthInterceptor(token))),
	)
	pb.RegisterPaymentServiceServer(srv, grpcserver.NewPaymentServer(orderSvc))
	reflection.Register(srv)

	go func() {
		log.Printf("gRPC listening on %s", addr)
		if err := srv.Serve(lis); err != nil {
			log.Printf("grpc: serve: %v", err)
		}
	}()

	return srv
}

func startHTTP(addr string, cfg config.Config, gw payment.PaymentGateway, q *db.Queries, rc *river.Client[pgx.Tx]) *http.Server {
	r := gin.Default()

	if mw := middleware.CORS(cfg.CorsOrigins); mw != nil {
		r.Use(mw)
	}

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "provider": cfg.Gateway})
	})

	webhookHandler := webhook.NewHandler(q, rc)
	webhookHandler.RegisterRoutes(r, map[string]payment.PaymentGateway{
		cfg.Gateway: gw,
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	go func() {
		log.Printf("HTTP listening on %s (provider=%s)", addr, cfg.Gateway)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("http: %v", err)
		}
	}()

	return srv
}

func newGateway(provider string) (payment.PaymentGateway, error) {
	switch provider {
	case "cashfree":
		return payment.NewCashfreeProvider(config.LoadCashfreeConfig())
	default:
		return nil, fmt.Errorf("unknown payment provider: %s", provider)
	}
}
