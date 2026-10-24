package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/efehidir/ddo/backend/internal/config"
	"github.com/efehidir/ddo/backend/internal/database"
	"github.com/efehidir/ddo/backend/internal/server"
)

type mockProbe struct {
	fail bool
}

func (m *mockProbe) Health(_ context.Context) (database.HealthReport, error) {
	if m.fail {
		return database.HealthReport{
			Status:    "down",
			Driver:    "pgx/v5",
			LatencyMs: 1.2,
			Error:     "connection refused",
		}, errors.New("connection refused")
	}
	return database.HealthReport{
		Status:    "up",
		Driver:    "pgx/v5",
		LatencyMs: 0.45,
		Pool: database.PoolStats{
			TotalConns:    5,
			IdleConns:     4,
			AcquiredConns: 1,
			MaxConns:      25,
		},
	}, nil
}

func (m *mockProbe) Diagnostics(_ context.Context) (database.DiagnosticReport, error) {
	if m.fail {
		return database.DiagnosticReport{
			Connected: false,
			Driver:    "pgx/v5",
			LatencyMs: 1.5,
			Error:     "database unreachable",
		}, errors.New("database unreachable")
	}
	return database.DiagnosticReport{
		Connected:       true,
		Driver:          "pgx/v5",
		ServerVersion:   "PostgreSQL 16.4",
		CurrentDatabase: "ddo_db",
		CurrentUser:     "ddo_user",
		ServerTime:      time.Now().UTC(),
		LatencyMs:       0.85,
		TotalChecks:     7,
		Pool: database.PoolStats{
			TotalConns:    5,
			IdleConns:     4,
			AcquiredConns: 1,
			MaxConns:      25,
		},
	}, nil
}

func (m *mockProbe) Close() {}

func TestHealthEndpoint_Healthy(t *testing.T) {
	cfg := config.Load()
	app := server.New(cfg, &mockProbe{fail: false})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var payload server.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	if payload.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %q", payload.Status)
	}
	if payload.Database.Status != "up" {
		t.Errorf("expected database status 'up', got %q", payload.Database.Status)
	}
}

func TestHealthEndpoint_Degraded(t *testing.T) {
	cfg := config.Load()
	app := server.New(cfg, &mockProbe{fail: true})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", resp.StatusCode)
	}

	var payload server.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	if payload.Status != "degraded" {
		t.Errorf("expected status 'degraded', got %q", payload.Status)
	}
	if payload.Database.Status != "down" {
		t.Errorf("expected database status 'down', got %q", payload.Database.Status)
	}
}

func TestDatabaseCheckEndpoint(t *testing.T) {
	cfg := config.Load()
	app := server.New(cfg, &mockProbe{fail: false})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/db/check", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var diag database.DiagnosticReport
	if err := json.NewDecoder(resp.Body).Decode(&diag); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	if !diag.Connected {
		t.Errorf("expected connected=true, got false")
	}
	if !strings.Contains(diag.ServerVersion, "PostgreSQL 16") {
		t.Errorf("expected PostgreSQL 16 version, got %q", diag.ServerVersion)
	}
}

func TestMetricsAndOpenAPIEndpoints(t *testing.T) {
	cfg := config.Load()
	app := server.New(cfg, &mockProbe{fail: false})

	// Trigger a health request first so metrics are populated
	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	_, _ = app.Test(healthReq, -1)

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsResp, err := app.Test(metricsReq, -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = metricsResp.Body.Close() }()

	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 on /metrics, got %d", metricsResp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(metricsResp.Body)
	if err != nil {
		t.Fatalf("failed to read /metrics body: %v", err)
	}
	body := string(bodyBytes)

	for _, metricName := range []string{"http_requests_total", "http_request_duration_seconds", "db_up"} {
		if !strings.Contains(body, metricName) {
			t.Errorf("expected metric %q in /metrics output", metricName)
		}
	}

	// OpenAPI JSON check
	specReq := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	specResp, err := app.Test(specReq, -1)
	if err != nil {
		t.Fatalf("unexpected error on /openapi.json: %v", err)
	}
	defer func() { _ = specResp.Body.Close() }()
	if specResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 on /openapi.json, got %d", specResp.StatusCode)
	}
}

