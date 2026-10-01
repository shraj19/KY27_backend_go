package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth"

	pb "ky27/backend/gen/payment/v1"
	"ky27/backend/internal/config"
	"ky27/backend/internal/db"
	grpcserver "ky27/backend/internal/grpc"
	"ky27/backend/internal/middleware"
	"ky27/backend/internal/order"
	"ky27/backend/internal/payment"
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

	// Start gRPC server in background
	grpcAddr := fmt.Sprintf(":%d", cfg.GRPCPort)
	go runGRPC(grpcAddr, orderSvc, cfg.ServiceToken)

	// Start HTTP server (health + future webhooks)
	httpAddr := fmt.Sprintf(":%d", cfg.Port)
	runHTTP(httpAddr, cfg)
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
	reflection.Register(srv) // Enable reflection for grpcurl/testing

	log.Printf("gRPC listening on %s", addr)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("grpc: serve: %v", err)
	}
}

func runHTTP(addr string, cfg config.Config) {
	r := gin.Default()

	if mw := middleware.CORS(cfg.CorsOrigins); mw != nil {
		r.Use(mw)
	}

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "provider": cfg.Gateway})
	})

	// Webhooks will be added here later

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
