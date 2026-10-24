package metrics

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Collector holds custom and standard Prometheus metrics for the backend service.
type Collector struct {
	registry        *prometheus.Registry
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	requestsInFlight prometheus.Gauge
	dbUp            prometheus.Gauge
	dbPingDuration  prometheus.Gauge
}

// NewCollector creates a dedicated Prometheus registry with Go runtime, process, HTTP, and DB metrics.
func NewCollector() *Collector {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed by the Go Fiber server.",
		},
		[]string{"method", "route", "status"},
	)

	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Histogram of HTTP request latencies in seconds.",
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"method", "route", "status"},
	)

	requestsInFlight := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Current number of HTTP requests being served.",
		},
	)

	dbUp := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "db_up",
			Help: "Whether the PostgreSQL 16 connection is healthy (1 = up, 0 = down).",
		},
	)

	dbPingDuration := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "db_ping_duration_seconds",
			Help: "Most recent PostgreSQL ping latency in seconds.",
		},
	)

	reg.MustRegister(requestsTotal, requestDuration, requestsInFlight, dbUp, dbPingDuration)

	return &Collector{
		registry:         reg,
		requestsTotal:    requestsTotal,
		requestDuration:  requestDuration,
		requestsInFlight: requestsInFlight,
		dbUp:             dbUp,
		dbPingDuration:   dbPingDuration,
	}
}

// Middleware records HTTP request count, latency, and in-flight requests.
func (c *Collector) Middleware() fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		start := time.Now()
		c.requestsInFlight.Inc()
		defer c.requestsInFlight.Dec()

		err := ctx.Next()

		routePath := ctx.Path()
		if r := ctx.Route(); r != nil && r.Path != "" {
			routePath = r.Path
		}

		status := strconv.Itoa(ctx.Response().StatusCode())
		method := ctx.Method()
		elapsed := time.Since(start).Seconds()

		c.requestsTotal.WithLabelValues(method, routePath, status).Inc()
		c.requestDuration.WithLabelValues(method, routePath, status).Observe(elapsed)

		return err
	}
}

// RecordDBHealth updates the Prometheus gauges for PostgreSQL connectivity and latency.
func (c *Collector) RecordDBHealth(up bool, latencyMs float64) {
	if up {
		c.dbUp.Set(1)
	} else {
		c.dbUp.Set(0)
	}
	c.dbPingDuration.Set(latencyMs / 1000.0)
}

// Handler returns the Fiber handler exposing Prometheus metrics at /metrics.
func (c *Collector) Handler() fiber.Handler {
	return adaptor.HTTPHandler(promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))
}

