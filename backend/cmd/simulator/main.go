package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/efehidir/ddo/backend/internal/demogate"
)

type simClient struct {
	baseURL    string
	httpClient *http.Client
	rng        *rand.Rand
	tokens     map[string]string
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	apiURL := os.Getenv("API_URL")
	if apiURL == "" {
		apiURL = "http://backend:8080"
	}

	seedVal := int64(42)
	if s := os.Getenv("SIMULATOR_SEED"); s != "" {
		if parsed, err := strconv.ParseInt(s, 10, 64); err == nil {
			seedVal = parsed
		}
	}

	intervalSec := 6
	if s := os.Getenv("SIMULATOR_INTERVAL_SEC"); s != "" {
		if parsed, err := strconv.Atoi(s); err == nil && parsed > 0 {
			intervalSec = parsed
		}
	}

	oneShot := os.Getenv("SIMULATOR_ONESHOT") == "1"

	slog.Info("starting SofraPOS deterministic 8-branch simulator",
		"api_url", apiURL,
		"seed", seedVal,
		"interval_sec", intervalSec,
	)

	sim := &simClient{
		baseURL:    apiURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		rng:        rand.New(rand.NewSource(seedVal)),
		tokens:     make(map[string]string),
	}

	sim.waitForBackend()
	if err := sim.authenticateAllRoles(); err != nil {
		slog.Error("simulator failed to authenticate roles", "error", err)
		os.Exit(1)
	}

	// Initial burst across all 8 branches so the dashboard is immediately lively
	if err := sim.withDemoGate(sim.runInitialEveningRush); err != nil {
		slog.Error("initial workload failed", "error", err)
		os.Exit(1)
	}

	if oneShot {
		slog.Info("simulator one-shot burst completed")
		return
	}

	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	tickCount := 1
	for range ticker.C {
		if tickCount%300 == 0 {
			if err := sim.authenticateAllRoles(); err != nil {
				slog.Error("simulator token renewal failed", "error", err)
				continue
			}
		}
		if err := sim.withDemoGate(func() { sim.runPeriodicTick(tickCount) }); err != nil {
			slog.Error("simulator workload gate failed", "error", err)
			continue
		}
		tickCount++
	}
}

func (s *simClient) withDemoGate(work func()) error {
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	release, err := demogate.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	work()
	return nil
}

func (s *simClient) waitForBackend() {
	for i := 0; i < 60; i++ {
		resp, err := s.httpClient.Get(s.baseURL + "/health")
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(1 * time.Second)
	}
}

func (s *simClient) authenticateAllRoles() error {
	// This system workload spans eight branches; ordinary demo staff stay branch-bound.
	for _, email := range []string{"merkez@sofrapos.local"} {
		body, status, err := s.postJSON("/api/v1/auth/login", "", "", map[string]any{
			"email":    email,
			"password": "Sofra2026!",
		})
		if err != nil || status != http.StatusOK {
			return fmt.Errorf("login failed for %s: status=%d err=%v", email, status, err)
		}
		var res struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.Unmarshal(body, &res)
		if res.AccessToken == "" {
			return fmt.Errorf("empty simulator access token")
		}
		for _, role := range []string{"waiter", "cashier", "chef", "branch_manager", "hq_manager"} {
			s.tokens[role] = res.AccessToken
		}
	}
	return nil
}

func (s *simClient) runInitialEveningRush() {
	// Deterministically seed active orders, KDS tickets, cancellations and payments across 8 branches
	var workers sync.WaitGroup
	for branchID := int64(1); branchID <= 8; branchID++ {
		workers.Add(1)
		go func(branchID int64) {
			defer workers.Done()
			s.runBranchRush(branchID)
		}(branchID)
	}
	workers.Wait()
	slog.Info("initial concurrent 8-branch evening rush completed")
}

func (s *simClient) runBranchRush(branchID int64) {
	tables := s.fetchBranchTables(branchID)
	if len(tables) < 4 {
		return
	}

	// Open Table 1 and Table 2 in each branch
	order1 := s.openOrder(branchID, tables[0])
	if order1 > 0 {
		// Add İskender (1.5 Porsiyon + Ekstra Tereyağı), Lahmacun x3, Ayran, Künefe
		_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/items", order1), s.tokens["waiter"], "", map[string]any{
			"menu_item_id": 1,
			"quantity":     1,
			"variant_id":   2,
			"modifier_ids": []int64{1},
		})
		_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/items", order1), s.tokens["waiter"], "", map[string]any{
			"menu_item_id": 2,
			"quantity":     3,
			"variant_id":   4,
		})
		_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/items", order1), s.tokens["waiter"], "", map[string]any{
			"menu_item_id": 4,
			"quantity":     1,
		})
		_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/items", order1), s.tokens["waiter"], "", map[string]any{
			"menu_item_id": 5,
			"quantity":     1,
		})

		// Apply best campaigns + SOFRA50 coupon
		_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/apply-campaigns", order1), s.tokens["cashier"], "", map[string]any{
			"coupon_code": "SOFRA50",
		})

		// For even branches, complete payment so HQ report shows live revenue across branches
		if branchID%2 == 0 {
			_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/payments", order1), s.tokens["cashier"], fmt.Sprintf("sim-init-pay-b%d", branchID), map[string]any{
				"method":     "CARD",
				"split_mode": "FULL",
				"tip_kurus":  int64(2500 + branchID*200),
			})
		}
	}

	// Open second table for active KDS tickets
	order2 := s.openOrder(branchID, tables[1])
	if order2 > 0 {
		_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/items", order2), s.tokens["waiter"], "", map[string]any{
			"menu_item_id": 3, // Atom Haydari (cold, course 1)
			"quantity":     1,
		})
		_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/items", order2), s.tokens["waiter"], "", map[string]any{
			"menu_item_id": 1, // İskender (hot, course 2)
			"quantity":     2,
		})
	}
}

func (s *simClient) runPeriodicTick(tick int) {
	branchID := int64((s.rng.Intn(8)) + 1)
	tables := s.fetchBranchTables(branchID)
	if len(tables) < 6 {
		return
	}

	tableIdx := 2 + (s.rng.Intn(4)) // Tables 3..6
	orderID := s.openOrder(branchID, tables[tableIdx])
	if orderID <= 0 {
		return
	}

	menuItemID := int64((s.rng.Intn(5)) + 1)
	qty := 1 + s.rng.Intn(2)

	body, status, err := s.postJSON(fmt.Sprintf("/api/v1/orders/%d/items", orderID), s.tokens["waiter"], "", map[string]any{
		"menu_item_id": menuItemID,
		"quantity":     qty,
	})
	if err != nil || status >= 300 {
		return
	}

	// Occasionally advance KDS status or cancel an item (fire) or move table
	actionRoll := s.rng.Intn(4)
	switch actionRoll {
	case 0:
		// Advance KDS item to PREPARING
		var detail struct {
			Items []struct {
				ID          int64 `json:"id"`
				IsCancelled bool  `json:"is_cancelled"`
			} `json:"items"`
		}
		_ = json.Unmarshal(body, &detail)
		if len(detail.Items) > 0 {
			lastItem := detail.Items[len(detail.Items)-1]
			_, _, _ = s.patchJSON(fmt.Sprintf("/api/v1/kds/items/%d/status", lastItem.ID), s.tokens["chef"], map[string]any{
				"status": "PREPARING",
			})
		}
	case 1:
		// Cancel produced item (waste/fire)
		var detail struct {
			Items []struct {
				ID          int64 `json:"id"`
				IsCancelled bool  `json:"is_cancelled"`
			} `json:"items"`
		}
		_ = json.Unmarshal(body, &detail)
		if len(detail.Items) > 1 {
			target := detail.Items[len(detail.Items)-1]
			if !target.IsCancelled {
				_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/items/%d/cancel", orderID, target.ID), s.tokens["branch_manager"], "", map[string]any{
					"reason":        "Simülatör mutfak fire kaydı",
					"produce_waste": true,
				})
			}
		}
	case 2:
		// Move table to an empty table if available
		if len(tables) >= 9 {
			_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/move", orderID), s.tokens["waiter"], "", map[string]any{
				"target_table_id": tables[8],
			})
		}
	case 3:
		// Pay order
		_, _, _ = s.postJSON(fmt.Sprintf("/api/v1/orders/%d/payments", orderID), s.tokens["cashier"], fmt.Sprintf("sim-tick-%d-o%d", tick, orderID), map[string]any{
			"method":     "CASH",
			"split_mode": "FULL",
			"tip_kurus":  1000,
		})
	}

	slog.Info("simulator tick completed", "tick", tick, "branch_id", branchID, "order_id", orderID)
}

func (s *simClient) fetchBranchTables(branchID int64) []int64 {
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/tables?branch_id=%d", s.baseURL, branchID), nil)
	req.Header.Set("Authorization", "Bearer "+s.tokens["waiter"])
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		slog.Error("simulator table query rejected", "branch_id", branchID, "status", resp.StatusCode)
		return nil
	}
	var data struct {
		Tables []struct {
			ID int64 `json:"id"`
		} `json:"tables"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&data)
	out := make([]int64, 0, len(data.Tables))
	for _, t := range data.Tables {
		out = append(out, t.ID)
	}
	return out
}

func (s *simClient) openOrder(branchID, tableID int64) int64 {
	body, status, err := s.postJSON("/api/v1/orders", s.tokens["waiter"], "", map[string]any{
		"branch_id": branchID,
		"table_id":  tableID,
	})
	if err != nil || (status != http.StatusOK && status != http.StatusCreated) {
		return 0
	}
	var res struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(body, &res)
	return res.ID
}

func (s *simClient) postJSON(path, token, idempKey string, payload any) ([]byte, int, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, s.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idempKey != "" {
		req.Header.Set("Idempotency-Key", idempKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		slog.Warn("simulator operation rejected", "path", path, "status", resp.StatusCode)
	}
	return data, resp.StatusCode, nil
}

func (s *simClient) patchJSON(path, token string, payload any) ([]byte, int, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPatch, s.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		slog.Warn("simulator operation rejected", "path", path, "status", resp.StatusCode)
	}
	return data, resp.StatusCode, nil
}
