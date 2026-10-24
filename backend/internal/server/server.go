package server

import (
	"math"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"

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

// New creates and configures a Fiber HTTP server with health, DB check, OpenAPI, and Prometheus routes.
func New(cfg config.Config, db database.Probe) *fiber.App {
	startedAt := time.Now()
	collector := metrics.NewCollector()

	app := fiber.New(fiber.Config{
		AppName:               "ddo-backend",
		DisableStartupMessage: true,
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          10 * time.Second,
		IdleTimeout:           30 * time.Second,
	})

	app.Use(recover.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.CORSAllowOrigins,
		AllowMethods: "GET,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept",
	}))
	app.Use(collector.Middleware())

	app.Get("/", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"service":     "ddo-backend",
			"version":     cfg.AppVersion,
			"environment": cfg.AppEnv,
			"endpoints": fiber.Map{
				"health":       "/health",
				"db_check":     "/api/v1/db/check",
				"metrics":      "/metrics",
				"docs":         "/docs",
				"openapi_yaml": "/openapi.yaml",
				"openapi_json": "/openapi.json",
			},
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
			Service:       "ddo-backend",
			Version:       cfg.AppVersion,
			Environment:   cfg.AppEnv,
			Timestamp:     time.Now().UTC(),
			UptimeSeconds: uptime,
			Database:      dbReport,
		})
	})

	app.Get("/api/v1/db/check", func(c *fiber.Ctx) error {
		diag, err := db.Diagnostics(c.UserContext())
		collector.RecordDBHealth(err == nil, diag.LatencyMs)

		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(diag)
		}
		return c.Status(fiber.StatusOK).JSON(diag)
	})

	app.Get("/metrics", collector.Handler())

	openapi.RegisterRoutes(app)

	return app
}

