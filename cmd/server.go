package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"ky27/backend/internal/config"
	"ky27/backend/internal/db"
	"ky27/backend/internal/middleware"
	"ky27/backend/internal/order"
	"ky27/backend/internal/payment"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	gateway, err := payment.NewGateway(cfg.Gateway, cfg.GatewayCfg)
	if err != nil {
		log.Fatalf("gateway: %v", err)
	}

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	queries := db.New(pool)
	_ = order.NewService(gateway, queries, cfg.Gateway)
	// orderSvc will be wired into gRPC in bucket B.

	r := gin.Default()

	if mw := middleware.CORS(cfg.CorsOrigins); mw != nil {
		r.Use(mw)
	}

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "provider": cfg.Gateway})
	})

	// gRPC service will be added in bucket B.

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("listening on %s (provider=%s)", addr, cfg.Gateway)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}
