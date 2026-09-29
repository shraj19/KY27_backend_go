package main

import (
	"fmt"
	"log"
	"strings"

	"ky27/backend/internal/config"
	"ky27/backend/internal/httpapi"
	"ky27/backend/internal/payment"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Build the selected payment gateway (the Strategy, via the switch factory).
	gateway, err := payment.NewGateway(cfg.Payment)
	if err != nil {
		log.Fatalf("payment: %v", err)
	}
	log.Printf("payment provider ready: %s", cfg.Payment.Provider)

	app := fiber.New()
	app.Use(logger.New())
	if len(cfg.CorsOrigins) > 0 {
		app.Use(cors.New(cors.Config{
			AllowOrigins: strings.Join(cfg.CorsOrigins, ","),
			AllowHeaders: "Origin, Content-Type, Accept, Authorization",
			AllowMethods: "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		}))
	}

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "provider": cfg.Payment.Provider})
	})

	// Payment routes (handler holds the injected gateway).
	httpapi.NewHandler(gateway).Register(app)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("listening on %s", addr)
	if err := app.Listen(addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}
