package server

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/efehidir/ddo/backend/internal/auth"
	"github.com/efehidir/ddo/backend/internal/config"
	"github.com/efehidir/ddo/backend/internal/database"
	"github.com/efehidir/ddo/backend/internal/metrics"
	"github.com/efehidir/ddo/backend/internal/openapi"
)

// HealthResponse is the JSON payload returned by GET /health.
type HealthResponse struct {
	Status        string                `json:"status"`
	Service       string                `json:"service"`
	Version       string                `json:"version"`
	Environment   string                `json:"environment"`
	Timestamp     time.Time             `json:"timestamp"`
	UptimeSeconds float64               `json:"uptime_seconds"`
	Database      database.HealthReport `json:"database"`
}

// New creates and configures a Fiber HTTP server with SofraPOS routes, health, OpenAPI, and Prometheus metrics.
func New(cfg config.Config, db database.Probe) *fiber.App {
	startedAt := time.Now()
	collector := metrics.NewCollector()

	app := fiber.New(fiber.Config{
		AppName:               "sofrapos-backend",
		DisableStartupMessage: true,
		ReadTimeout:           15 * time.Second,
		WriteTimeout:          30 * time.Second,
		IdleTimeout:           60 * time.Second,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			status, code, message := 500, "INTERNAL_ERROR", "İşlem tamamlanamadı. Lütfen tekrar deneyin."
			var httpErr *fiber.Error
			if errors.As(err, &httpErr) {
				status, code, message = httpErr.Code, "HTTP_ERROR", httpErr.Message
			} else if errors.Is(err, context.DeadlineExceeded) {
				status, code, message = 504, "TIMEOUT", "İşlem zaman aşımına uğradı."
			}
			slog.Error("request failed", "method", c.Method(), "path", c.Path(), "status", status, "error", err)
			return auth.RespondError(c, status, code, message)
		},
	})

	app.Use(recover.New())

	// Mandatory header on every HTTP response: X-DDO-Case: cc6433
	app.Use(func(c *fiber.Ctx) error {
		c.Set("X-DDO-Case", "cc6433")
		return c.Next()
	})

	app.Use(cors.New(cors.Config{
		AllowOrigins:  cfg.CORSAllowOrigins,
		AllowMethods:  "GET,POST,PATCH,PUT,DELETE,OPTIONS",
		AllowHeaders:  "Origin,Content-Type,Accept,Authorization,Idempotency-Key,Last-Event-ID",
		ExposeHeaders: "X-DDO-Case,X-Idempotent-Replay",
	}))
	app.Use(collector.Middleware())
	app.Use(func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), 15*time.Second)
		defer cancel()
		c.SetUserContext(ctx)
		return c.Next()
	})

	app.Get("/", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"service":     "sofrapos-backend",
			"case":        "cc6433",
			"version":     cfg.AppVersion,
			"environment": cfg.AppEnv,
			"endpoints": fiber.Map{
				"health":       "/health",
				"hz_cc64":      "/_hz/cc64",
				"db_check":     "/api/v1/db/check",
				"metrics":      "/metrics",
				"docs":         "/docs",
				"openapi_yaml": "/openapi.yaml",
				"openapi_json": "/openapi.json",
				"pos_api":      "/api/v1",
			},
		})
	})

	// Additional health endpoint required by internal spec: GET /_hz/cc64
	app.Get("/_hz/cc64", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"status": "ok",
			"case":   "cc6433",
		})
	})

	app.Get("/health", func(c *fiber.Ctx) error {
		dbReport, err := db.Health(c.UserContext())
		collector.RecordDBHealth(err == nil, dbReport.LatencyMs)

		uptime := math.Round(time.Since(startedAt).Seconds()*100) / 100
		status := "healthy"
		httpCode := fiber.StatusOK
		if err != nil {
			status = "degraded"
			httpCode = fiber.StatusServiceUnavailable
		}

		return c.Status(httpCode).JSON(HealthResponse{
			Status:        status,
			Service:       "sofrapos-backend",
			Version:       cfg.AppVersion,
			Environment:   cfg.AppEnv,
			Timestamp:     time.Now().UTC(),
			UptimeSeconds: uptime,
			Database:      dbReport,
		})
	})

	app.Get("/api/v1/db/check", auth.RequireAuth(cfg.JWTSecret), auth.RequireRoles(auth.RoleHQManager), func(c *fiber.Ctx) error {
		diag, err := db.Diagnostics(c.UserContext())
		collector.RecordDBHealth(err == nil, diag.LatencyMs)

		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(diag)
		}
		return c.Status(fiber.StatusOK).JSON(diag)
	})

	app.Get("/metrics", collector.Handler())

	openapi.RegisterRoutes(app)

	posService := NewPOSService(cfg, db)
	posService.RegisterPOSRoutes(app)

	return app
}
