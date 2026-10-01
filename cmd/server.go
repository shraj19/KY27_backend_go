package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"

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
	defer pool.Close()

	queries := db.New(pool)
	orderSvc := order.NewService(gateway, queries, cfg.Gateway)

	// Set up River job queue
	riverClient, err := setupRiver(context.Background(), pool, cfg.NodeWebhookURL)
	if err != nil {
		log.Fatalf("river: %v", err)
	}
	defer riverClient.Stop(context.Background())

	// Start gRPC server in background
	grpcAddr := fmt.Sprintf(":%d", cfg.GRPCPort)
	go runGRPC(grpcAddr, orderSvc, cfg.ServiceToken)

	// Start HTTP server (health + webhooks)
	httpAddr := fmt.Sprintf(":%d", cfg.Port)
	runHTTP(httpAddr, cfg, gateway, queries, riverClient)
}

func setupRiver(ctx context.Context, pool *pgxpool.Pool, nodeWebhookURL string) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewNotifyNodeWorker(nodeWebhookURL))

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

func runGRPC(addr string, orderSvc *order.Service, token string) {
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

	log.Printf("gRPC listening on %s", addr)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("grpc: serve: %v", err)
	}
}

func runHTTP(addr string, cfg config.Config, gw payment.PaymentGateway, q *db.Queries, rc *river.Client[pgx.Tx]) {
	r := gin.Default()

	if mw := middleware.CORS(cfg.CorsOrigins); mw != nil {
		r.Use(mw)
	}

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "provider": cfg.Gateway})
	})

	// Register webhook routes
	webhookHandler := webhook.NewHandler(gw, q, rc)
	webhookHandler.RegisterRoutes(r)

	log.Printf("HTTP listening on %s (provider=%s)", addr, cfg.Gateway)
	if err := r.Run(addr); err != nil {
		log.Fatalf("http: %v", err)
	}
}

func newGateway(provider string) (payment.PaymentGateway, error) {
	switch provider {
	case "cashfree":
		return payment.NewCashfreeProvider(config.LoadCashfreeConfig())
	default:
		return nil, fmt.Errorf("unknown payment provider: %s", provider)
	}
}
