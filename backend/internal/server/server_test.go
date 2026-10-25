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

	"github.com/efehidir/ddo/backend/internal/auth"
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

func TestHealthEndpoint_HealthyAndHeader(t *testing.T) {
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

	if gotHeader := resp.Header.Get("X-DDO-Case"); gotHeader != "cc6433" {
		t.Errorf("expected X-DDO-Case header 'cc6433', got %q", gotHeader)
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

func TestHzCC64Endpoint(t *testing.T) {
	cfg := config.Load()
	app := server.New(cfg, &mockProbe{fail: false})

	req := httptest.NewRequest(http.MethodGet, "/_hz/cc64", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 on /_hz/cc64, got %d", resp.StatusCode)
	}
	if gotHeader := resp.Header.Get("X-DDO-Case"); gotHeader != "cc6433" {
		t.Errorf("expected X-DDO-Case header 'cc6433', got %q", gotHeader)
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
	pair, _ := auth.GenerateTokenPair(cfg.JWTSecret, 1, 1, "hq@test.invalid", "HQ", auth.RoleHQManager)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
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

func TestDemoAccountsAndRBACProtection(t *testing.T) {
	cfg := config.Load()
	app := server.New(cfg, &mockProbe{fail: false})

	// 1. Demo accounts list includes seed.ceylan@ddo-case.invalid
	demoReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/demo-accounts", nil)
	demoResp, err := app.Test(demoReq, -1)
	if err != nil || demoResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/auth/demo-accounts")
	}
	bodyBytes, _ := io.ReadAll(demoResp.Body)
	_ = demoResp.Body.Close()
	if !strings.Contains(string(bodyBytes), "seed.ceylan@ddo-case.invalid") {
		t.Errorf("expected demo accounts to include seed.ceylan@ddo-case.invalid")
	}

	// 2. Unauthenticated request to /api/v1/reports/hq -> 401
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/v1/reports/hq", nil)
	unauthResp, _ := app.Test(unauthReq, -1)
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 on unauthenticated /api/v1/reports/hq, got %d", unauthResp.StatusCode)
	}

	// 3. Waiter trying to access HQ report -> 403
	waiterTokens, _ := auth.GenerateTokenPair(cfg.JWTSecret, 2, 1, "garson@sofrapos.local", "Ahmet", auth.RoleWaiter)
	forbReq := httptest.NewRequest(http.MethodGet, "/api/v1/reports/hq", nil)
	forbReq.Header.Set("Authorization", "Bearer "+waiterTokens.AccessToken)
	forbResp, _ := app.Test(forbReq, -1)
	if forbResp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for waiter on /api/v1/reports/hq, got %d", forbResp.StatusCode)
	}

	// 4. Customer trying to create payment -> 403
	custTokens, _ := auth.GenerateTokenPair(cfg.JWTSecret, 7, 1, "musteri@sofrapos.local", "Elif", auth.RoleCustomer)
	payReq := httptest.NewRequest(http.MethodPost, "/api/v1/orders/1/payments", nil)
	payReq.Header.Set("Authorization", "Bearer "+custTokens.AccessToken)
	payResp, _ := app.Test(payReq, -1)
	if payResp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for customer on payment endpoint, got %d", payResp.StatusCode)
	}
}

func TestMetricsAndOpenAPIEndpoints(t *testing.T) {
	cfg := config.Load()
	app := server.New(cfg, &mockProbe{fail: false})

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
