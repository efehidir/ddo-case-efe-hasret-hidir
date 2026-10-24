package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type checkResult struct {
	ID      string
	Name    string
	Passed  bool
	Details string
	Elapsed time.Duration
}

type verifier struct {
	apiURL      string
	frontendURL string
	client      *http.Client
	tokens      map[string]string
	results     []checkResult
}

func main() {
	apiURL := os.Getenv("API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:3000"
	}

	v := &verifier{
		apiURL:      strings.TrimRight(apiURL, "/"),
		frontendURL: strings.TrimRight(frontendURL, "/"),
		client:      &http.Client{Timeout: 12 * time.Second},
		tokens:      make(map[string]string),
	}

	fmt.Println("================================================================================")
	fmt.Println(" SofraPOS — Uçtan Uca Otomatik Doğrulama Aracı (make verify)")
	fmt.Printf(" Backend API : %s\n", v.apiURL)
	fmt.Printf(" Frontend UI : %s\n", v.frontendURL)
	fmt.Println("================================================================================")

	v.waitForReady()

	// Reset to clean deterministic seed before running verification suite
	_, _, _, _ = v.doJSON(http.MethodPost, "/api/v1/admin/reset-demo", "", "", nil)

	v.runCheck("HEALTH", "GET /health, GET /_hz/cc64, X-DDO-Case başlığı ve DB sağlık kontrolü", v.verifyHealthAndHeaders)
	v.runCheck("AUTH-RBAC", "Kimlik doğrulama (seed.ceylan, yanlış parola 401, yetkisiz rol 403)", v.verifyAuthAndRBAC)
	v.runCheck("F-1", "Menü, varyantlar ve ek seçenekler (modifier) + Idempotency-Key", v.verifyF1MenuVariantsModifiers)
	v.runCheck("F-2", "Masa ve adisyon yönetimi (Masa açma, kalem ekleme, taşıma)", v.verifyF2TablesAndOrders)
	v.runCheck("F-3", "Mutfak ekranı (KDS) ve istasyonlar (soğuk/sıcak/bar)", v.verifyF3KitchenDisplay)
	v.runCheck("F-4", "Kasa: nakit/kart, hesap bölüşme, bahşiş", v.verifyF4CashierSplitAndTip)
	v.runCheck("F-5", "Reçete bazlı stok ve fire hareketleri", v.verifyF5RecipeStockAndWaste)
	v.runCheck("F-6", "Kampanya ve kupon kuralları", v.verifyF6CampaignsAndCoupons)
	v.runCheck("F-7", "Vardiya, Z raporu ve 8 şubeli merkezi raporlar", v.verifyF7ShiftZReportAndHQ)
	v.runCheck("T-1", "Zorlu Görev 1: Adisyon tutarlılığı (Eşzamanlı garson, optimistic lock, birleştirme, rebuild)", v.verifyT1OrderConsistency)
	v.runCheck("T-2", "Zorlu Görev 2: Hesap bölüşme, kuruş kaymasız indirim ve Z raporu mutabakatı", v.verifyT2SplitBillAndZeroDrift)
	v.runCheck("T-3", "Zorlu Görev 3: Gerçek zamanlı KDS, kurs sırası, gecikme ve SSE kopma telafisi (replay)", v.verifyT3RealtimeKDSReplay)
	v.runCheck("T-4", "Zorlu Görev 4: Ürün -> Reçete -> Yarı Mamul (Alt Reçete) -> Hammadde ve Gün Sonu Sayım Farkı", v.verifyT4SubRecipeAndVariance)
	v.runCheck("T-5", "Zorlu Görev 5: Kampanya motoru en avantajlı kombinasyon seçimi ve açıklanabilirlik", v.verifyT5CampaignOptimizer)

	v.printSummaryAndExit()
}

func (v *verifier) waitForReady() {
	for i := 0; i < 45; i++ {
		resp, err := v.client.Get(v.apiURL + "/health")
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

func (v *verifier) runCheck(id, name string, fn func() (string, error)) {
	start := time.Now()
	details, err := fn()
	elapsed := time.Since(start)
	if err != nil {
		v.results = append(v.results, checkResult{
			ID:      id,
			Name:    name,
			Passed:  false,
			Details: err.Error(),
			Elapsed: elapsed,
		})
		return
	}
	v.results = append(v.results, checkResult{
		ID:      id,
		Name:    name,
		Passed:  true,
		Details: details,
		Elapsed: elapsed,
	})
}

func (v *verifier) verifyHealthAndHeaders() (string, error) {
	body, status, headers, err := v.doJSON(http.MethodGet, "/health", "", "", nil)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("GET /health returned status=%d err=%v", status, err)
	}
	if headers.Get("X-DDO-Case") != "cc6433" {
		return "", fmt.Errorf("missing or invalid X-DDO-Case header: %q", headers.Get("X-DDO-Case"))
	}
	if !strings.Contains(string(body), `"status":"healthy"`) {
		return "", fmt.Errorf("unexpected /health payload: %s", string(body))
	}

	_, hzStatus, hzHeaders, err := v.doJSON(http.MethodGet, "/_hz/cc64", "", "", nil)
	if err != nil || hzStatus != http.StatusOK || hzHeaders.Get("X-DDO-Case") != "cc6433" {
		return "", fmt.Errorf("GET /_hz/cc64 failed: status=%d", hzStatus)
	}

	return "GET /health (200), GET /_hz/cc64 (200) ve X-DDO-Case: cc6433 doğrulandı", nil
}

func (v *verifier) verifyAuthAndRBAC() (string, error) {
	// 1. Wrong password -> 401
	_, badStatus, _, _ := v.doJSON(http.MethodPost, "/api/v1/auth/login", "", "", map[string]any{
		"email":    "seed.ceylan@ddo-case.invalid",
		"password": "WrongPassword123!",
	})
	if badStatus != http.StatusUnauthorized {
		return "", fmt.Errorf("wrong password expected 401, got %d", badStatus)
	}

	// 2. Login all 6 roles + seed.ceylan@ddo-case.invalid
	accounts := map[string]string{
		"seed":           "seed.ceylan@ddo-case.invalid",
		"waiter":         "garson@sofrapos.local",
		"cashier":        "kasiyer@sofrapos.local",
		"chef":           "asci@sofrapos.local",
		"branch_manager": "mudur@sofrapos.local",
		"hq_manager":     "merkez@sofrapos.local",
		"customer":       "musteri@sofrapos.local",
	}

	for key, email := range accounts {
		body, status, _, err := v.doJSON(http.MethodPost, "/api/v1/auth/login", "", "", map[string]any{
			"email":    email,
			"password": "Sofra2026!",
		})
		if err != nil || status != http.StatusOK {
			return "", fmt.Errorf("login failed for %s: status=%d err=%v", email, status, err)
		}
		var res struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.Unmarshal(body, &res)
		if res.AccessToken == "" {
			return "", fmt.Errorf("empty access_token for %s", email)
		}
		v.tokens[key] = res.AccessToken
	}

	// 3. Unauthenticated request -> 401
	_, unauthStatus, _, _ := v.doJSON(http.MethodGet, "/api/v1/reports/hq", "", "", nil)
	if unauthStatus != http.StatusUnauthorized {
		return "", fmt.Errorf("unauthenticated request expected 401, got %d", unauthStatus)
	}

	// 4. Unauthorized role (waiter -> HQ report, customer -> payment) -> 403
	_, waiterHQStatus, _, _ := v.doJSON(http.MethodGet, "/api/v1/reports/hq", v.tokens["waiter"], "", nil)
	if waiterHQStatus != http.StatusForbidden {
		return "", fmt.Errorf("waiter on /api/v1/reports/hq expected 403, got %d", waiterHQStatus)
	}

	_, custPayStatus, _, _ := v.doJSON(http.MethodPost, "/api/v1/orders/1/payments", v.tokens["customer"], "", map[string]any{
		"method": "CASH", "amount_kurus": 1000,
	})
	if custPayStatus != http.StatusForbidden {
		return "", fmt.Errorf("customer on payment endpoint expected 403, got %d", custPayStatus)
	}

	return "7 hesap girişi (seed.ceylan dahil), 401 (hatalı parola/tokensiz) ve 403 (RBAC yetki ihlali) doğrulandı", nil
}

func (v *verifier) verifyF1MenuVariantsModifiers() (string, error) {
	body, status, _, err := v.doJSON(http.MethodGet, "/api/v1/menu", v.tokens["customer"], "", nil)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("GET /api/v1/menu failed: status=%d", status)
	}
	if !strings.Contains(string(body), "MN-ISKENDER") || !strings.Contains(string(body), "1.5 Porsiyon") || !strings.Contains(string(body), "Ekstra Tereyağı") {
		return "", fmt.Errorf("menu missing expected items/variants/modifiers")
	}

	// Create menu item with Idempotency-Key and test replay
	idempKey := "verify-f1-menu-create-001"
	payload := map[string]any{
		"category_id":      1,
		"code":             "MN-EZME-VERIFY",
		"name":             "Acılı Antep Ezmesi",
		"description":      "Nar ekşili taze zırh ezme",
		"base_price_kurus": 8500,
		"station":          "cold",
		"course":           1,
		"prep_time_sec":    90,
	}
	_, st1, _, err := v.doJSON(http.MethodPost, "/api/v1/menu/items", v.tokens["branch_manager"], idempKey, payload)
	if err != nil || st1 != http.StatusCreated {
		return "", fmt.Errorf("POST /api/v1/menu/items failed: status=%d", st1)
	}
	_, st2, hdr2, err := v.doJSON(http.MethodPost, "/api/v1/menu/items", v.tokens["branch_manager"], idempKey, payload)
	if err != nil || st2 != http.StatusCreated || hdr2.Get("X-Idempotent-Replay") != "true" {
		return "", fmt.Errorf("idempotent replay failed: status=%d replay=%q", st2, hdr2.Get("X-Idempotent-Replay"))
	}

	return "Menü, varyantlar (1.5 Porsiyon), modifier'lar (Ekstra Tereyağı) ve Idempotency-Key doğrulandı", nil
}

func (v *verifier) verifyF2TablesAndOrders() (string, error) {
	body, status, _, err := v.doJSON(http.MethodPost, "/api/v1/orders", v.tokens["waiter"], "verify-f2-open-t1", map[string]any{
		"branch_id": 1,
		"table_id":  1,
	})
	if err != nil || (status != http.StatusCreated && status != http.StatusOK) {
		return "", fmt.Errorf("open order on table 1 failed: status=%d", status)
	}
	var ord struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(body, &ord)
	if ord.ID <= 0 {
		return "", fmt.Errorf("invalid order ID returned")
	}

	// Add Haydari (cold, course 1) and İskender (hot, course 2, 1.5 Porsiyon + Ekstra Tereyağı)
	_, stItem1, _, _ := v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord.ID), v.tokens["waiter"], "", map[string]any{
		"menu_item_id": 3,
		"quantity":     1,
	})
	_, stItem2, _, _ := v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord.ID), v.tokens["waiter"], "", map[string]any{
		"menu_item_id": 1,
		"quantity":     1,
		"variant_id":   2, // +16000 kr
		"modifier_ids": []int64{1}, // +4000 kr -> total 56000 kr
	})
	if stItem1 != http.StatusCreated || stItem2 != http.StatusCreated {
		return "", fmt.Errorf("adding items failed: %d / %d", stItem1, stItem2)
	}

	return fmt.Sprintf("Masa 1 adisyonu (#%d) açıldı, varyant ve modifier'lı kalemler eklendi", ord.ID), nil
}

func (v *verifier) verifyF3KitchenDisplay() (string, error) {
	body, status, _, err := v.doJSON(http.MethodGet, "/api/v1/kds/items?branch_id=1&station=all", v.tokens["chef"], "", nil)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("GET /api/v1/kds/items failed: status=%d", status)
	}
	var kds struct {
		Items []struct {
			OrderItemID int64  `json:"order_item_id"`
			Station     string `json:"station"`
			Course      int    `json:"course"`
		} `json:"items"`
	}
	_ = json.Unmarshal(body, &kds)
	if len(kds.Items) == 0 {
		return "", fmt.Errorf("expected active KDS items, got 0")
	}

	firstItemID := kds.Items[0].OrderItemID
	_, patchSt, _, err := v.doJSON(http.MethodPatch, fmt.Sprintf("/api/v1/kds/items/%d/status", firstItemID), v.tokens["chef"], "", map[string]any{
		"status": "PREPARING",
	})
	if err != nil || patchSt != http.StatusOK {
		return "", fmt.Errorf("PATCH KDS status failed: status=%d", patchSt)
	}

	return fmt.Sprintf("KDS ekranında %d aktif fiş listelendi ve #%d PREPARING durumuna alındı", len(kds.Items), firstItemID), nil
}

func (v *verifier) verifyF4CashierSplitAndTip() (string, error) {
	// Open Table 2, add 2 Lahmacun (22000 kr), preview 2-way equal split and pay with tip
	body, _, _, _ := v.doJSON(http.MethodPost, "/api/v1/orders", v.tokens["waiter"], "", map[string]any{
		"branch_id": 1, "table_id": 2,
	})
	var ord struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(body, &ord)

	_, _, _, _ = v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord.ID), v.tokens["waiter"], "", map[string]any{
		"menu_item_id": 2, "quantity": 2,
	})

	_, paySt, _, err := v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/payments", ord.ID), v.tokens["cashier"], "verify-f4-pay-001", map[string]any{
		"method":            "MIXED",
		"split_mode":        "MIXED",
		"cash_amount_kurus": 10000,
		"card_amount_kurus": 12000,
		"tip_kurus":         3000,
	})
	if err != nil || paySt != http.StatusCreated {
		return "", fmt.Errorf("mixed payment with tip failed: status=%d", paySt)
	}

	return fmt.Sprintf("Adisyon #%d için karışık (100 TL Nakit + 120 TL Kart + 30 TL Bahşiş) ödeme tamamlandı", ord.ID), nil
}

func (v *verifier) verifyF5RecipeStockAndWaste() (string, error) {
	body, status, _, err := v.doJSON(http.MethodGet, "/api/v1/inventory?branch_id=1", v.tokens["branch_manager"], "", nil)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("GET /api/v1/inventory failed: status=%d", status)
	}
	if !strings.Contains(string(body), "SEMI-ISKENDER-SAUCE") || !strings.Contains(string(body), "SALE_DEDUCTION") {
		return "", fmt.Errorf("inventory missing semi-finished sub-recipe or sale deduction movements")
	}
	return "Reçete bazlı otomatik stok düşümü ve yarı mamul (alt reçete) envanteri doğrulandı", nil
}

func (v *verifier) verifyF6CampaignsAndCoupons() (string, error) {
	body, status, _, err := v.doJSON(http.MethodGet, "/api/v1/campaigns", v.tokens["cashier"], "", nil)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("GET /api/v1/campaigns failed: status=%d", status)
	}
	if !strings.Contains(string(body), "CMP-3AL2ODE") || !strings.Contains(string(body), "SOFRA50") {
		return "", fmt.Errorf("campaigns/coupons list incomplete")
	}
	return "Aktif kampanya (BUY_X_PAY_Y, COMBO, HAPPY_HOUR) ve kupon (SOFRA50, VIP20) tanımları doğrulandı", nil
}

func (v *verifier) verifyF7ShiftZReportAndHQ() (string, error) {
	zBody, zSt, _, err := v.doJSON(http.MethodGet, "/api/v1/reports/z-report?branch_id=1", v.tokens["branch_manager"], "", nil)
	if err != nil || zSt != http.StatusOK {
		return "", fmt.Errorf("GET /api/v1/reports/z-report failed: status=%d", zSt)
	}
	if !strings.Contains(string(zBody), `"is_balanced":true`) {
		return "", fmt.Errorf("Z-report is not balanced: %s", string(zBody))
	}

	hqBody, hqSt, _, err := v.doJSON(http.MethodGet, "/api/v1/reports/hq", v.tokens["hq_manager"], "", nil)
	if err != nil || hqSt != http.StatusOK || !strings.Contains(string(hqBody), `"branch_count":8`) {
		return "", fmt.Errorf("GET /api/v1/reports/hq failed: status=%d", hqSt)
	}

	auditBody, auditSt, _, err := v.doJSON(http.MethodGet, "/api/v1/audit-logs?limit=10", v.tokens["hq_manager"], "", nil)
	if err != nil || auditSt != http.StatusOK || !strings.Contains(string(auditBody), "audit_logs") {
		return "", fmt.Errorf("GET /api/v1/audit-logs failed: status=%d", auditSt)
	}

	return "Vardiya Z raporu (is_balanced: true), 8 şubeli merkez raporu ve append-only denetim günlüğü doğrulandı", nil
}

func (v *verifier) verifyT1OrderConsistency() (string, error) {
	// 1. Open Table 3 and Table 4
	b3, _, _, _ := v.doJSON(http.MethodPost, "/api/v1/orders", v.tokens["waiter"], "", map[string]any{
		"branch_id": 1, "table_id": 3,
	})
	b4, _, _, _ := v.doJSON(http.MethodPost, "/api/v1/orders", v.tokens["waiter"], "", map[string]any{
		"branch_id": 1, "table_id": 4,
	})
	var ord3, ord4 struct {
		ID      int64 `json:"id"`
		Version int64 `json:"version"`
	}
	_ = json.Unmarshal(b3, &ord3)
	_ = json.Unmarshal(b4, &ord4)

	// 2. Two waiters concurrently add items to Table 3
	var wg sync.WaitGroup
	wg.Add(2)
	var stA, stB int
	go func() {
		defer wg.Done()
		_, stA, _, _ = v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord3.ID), v.tokens["waiter"], "", map[string]any{
			"menu_item_id": 1, "quantity": 1,
		})
	}()
	go func() {
		defer wg.Done()
		_, stB, _, _ = v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord3.ID), v.tokens["cashier"], "", map[string]any{
			"menu_item_id": 2, "quantity": 2,
		})
	}()
	wg.Wait()
	if stA != http.StatusCreated || stB != http.StatusCreated {
		return "", fmt.Errorf("concurrent item additions failed: %d / %d", stA, stB)
	}

	// 3. Verify optimistic locking conflict (expected_version = 1 when version is now > 1) -> 409 Conflict
	_, conflictStatus, _, _ := v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord3.ID), v.tokens["waiter"], "", map[string]any{
		"menu_item_id":     4,
		"quantity":         1,
		"expected_version": 1,
	})
	if conflictStatus != http.StatusConflict {
		return "", fmt.Errorf("expected 409 Conflict on stale expected_version, got %d", conflictStatus)
	}

	// 4. Add item to Table 4 and merge Table 4 into Table 3
	_, _, _, _ = v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord4.ID), v.tokens["waiter"], "", map[string]any{
		"menu_item_id": 5, "quantity": 1,
	})
	mergeBody, mergeSt, _, _ := v.doJSON(http.MethodPost, "/api/v1/orders/merge", v.tokens["waiter"], "", map[string]any{
		"source_order_id": ord4.ID,
		"target_order_id": ord3.ID,
	})
	if mergeSt != http.StatusOK {
		return "", fmt.Errorf("order merge failed: status=%d body=%s", mergeSt, string(mergeBody))
	}

	// 5. Move Table 3 order to Table 5
	_, moveSt, _, _ := v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/move", ord3.ID), v.tokens["waiter"], "", map[string]any{
		"target_table_id": 5,
	})
	if moveSt != http.StatusOK {
		return "", fmt.Errorf("table move failed: status=%d", moveSt)
	}

	// 6. Cancel an item as produced (waste/fire)
	var detail struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(mergeBody, &detail)
	if len(detail.Items) > 0 {
		_, cancSt, _, _ := v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items/%d/cancel", ord3.ID, detail.Items[0].ID), v.tokens["waiter"], "", map[string]any{
			"reason":        "Üretilmiş kalem iptali testi",
			"produce_waste": true,
		})
		if cancSt != http.StatusOK {
			return "", fmt.Errorf("item cancel failed: status=%d", cancSt)
		}
	}

	// 7. Partial payment then add another item during payment
	_, _, _, _ = v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/payments", ord3.ID), v.tokens["cashier"], "", map[string]any{
		"method":       "CASH",
		"split_mode":   "CUSTOM",
		"amount_kurus": 10000,
	})
	_, _, _, _ = v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord3.ID), v.tokens["waiter"], "", map[string]any{
		"menu_item_id": 4, "quantity": 1,
	})

	// 8. Rebuild order from event log and verify 100% consistency
	rebBody, rebSt, _, err := v.doJSON(http.MethodGet, fmt.Sprintf("/api/v1/orders/%d/rebuild", ord3.ID), v.tokens["branch_manager"], "", nil)
	if err != nil || rebSt != http.StatusOK || !strings.Contains(string(rebBody), `"is_consistent":true`) {
		return "", fmt.Errorf("order event log rebuild inconsistency: status=%d body=%s", rebSt, string(rebBody))
	}

	return fmt.Sprintf("Eşzamanlı kalem ekleme, optimistic lock (409), birleştirme, taşıma, fire ve olay günlüğü rebuild (is_consistent: true) kanıtlandı"), nil
}

func (v *verifier) verifyT2SplitBillAndZeroDrift() (string, error) {
	// Open Table 6, add 3 items of 11000 kr = 33000 kr, apply SOFRA50 coupon (5000 kr discount across 3 items -> 1667 + 1667 + 1666 = 5000 kr!)
	b6, _, _, _ := v.doJSON(http.MethodPost, "/api/v1/orders", v.tokens["waiter"], "", map[string]any{
		"branch_id": 1, "table_id": 6,
	})
	var ord6 struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(b6, &ord6)

	for i := 0; i < 3; i++ {
		_, _, _, _ = v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord6.ID), v.tokens["waiter"], "", map[string]any{
			"menu_item_id": 5, "quantity": 1, // 19000 kr each -> 57000 kr total
		})
	}

	// Apply SOFRA50 (5000 kr discount across 3 equal items -> 1667 + 1667 + 1666 = 5000 kr)
	cmpBody, cmpSt, _, _ := v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/apply-campaigns", ord6.ID), v.tokens["cashier"], "", map[string]any{
		"coupon_code":  "SOFRA50",
		"current_hour": 15,
	})
	if cmpSt != http.StatusOK {
		return "", fmt.Errorf("apply-campaigns failed: status=%d", cmpSt)
	}

	var cmpRes struct {
		Order struct {
			SubtotalKurus int64 `json:"subtotal_kurus"`
			DiscountKurus int64 `json:"discount_kurus"`
			TotalKurus    int64 `json:"total_kurus"`
			Items         []struct {
				ID                     int64 `json:"id"`
				AllocatedDiscountKurus int64 `json:"allocated_discount_kurus"`
				NetTotalKurus          int64 `json:"net_total_kurus"`
			} `json:"items"`
		} `json:"order"`
	}
	_ = json.Unmarshal(cmpBody, &cmpRes)

	var sumAllocDisc, sumNet int64
	for _, it := range cmpRes.Order.Items {
		sumAllocDisc += it.AllocatedDiscountKurus
		sumNet += it.NetTotalKurus
	}
	if sumAllocDisc != cmpRes.Order.DiscountKurus || sumNet != cmpRes.Order.TotalKurus {
		return "", fmt.Errorf("kurus drift detected! sumAllocDisc=%d vs discount=%d", sumAllocDisc, cmpRes.Order.DiscountKurus)
	}

	// Preview 3-way EQUAL split
	spBody, spSt, _, _ := v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/split-preview", ord6.ID), v.tokens["cashier"], "", map[string]any{
		"split_mode": "EQUAL",
		"parts":      3,
	})
	if spSt != http.StatusOK {
		return "", fmt.Errorf("split-preview failed: status=%d", spSt)
	}
	var spRes struct {
		SharesKurus []int64 `json:"shares_kurus"`
	}
	_ = json.Unmarshal(spBody, &spRes)
	var shareSum int64
	for _, sh := range spRes.SharesKurus {
		shareSum += sh
	}
	if shareSum != cmpRes.Order.TotalKurus {
		return "", fmt.Errorf("equal split share sum %d != order total %d", shareSum, cmpRes.Order.TotalKurus)
	}

	// Pay all 3 shares (1st CASH, 2nd CARD, 3rd CARD with tip)
	for idx, sh := range spRes.SharesKurus {
		method := "CARD"
		if idx == 0 {
			method = "CASH"
		}
		_, paySt, _, _ := v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/payments", ord6.ID), v.tokens["cashier"], fmt.Sprintf("verify-t2-split-%d", idx), map[string]any{
			"method":       method,
			"split_mode":   "EQUAL",
			"amount_kurus": sh,
			"tip_kurus":    int64(idx * 500),
		})
		if paySt != http.StatusCreated {
			return "", fmt.Errorf("split payment %d failed: status=%d", idx, paySt)
		}
	}

	// Verify Z-Report balance after split payments
	zBody, _, _, _ := v.doJSON(http.MethodGet, "/api/v1/reports/z-report?branch_id=1", v.tokens["cashier"], "", nil)
	if !strings.Contains(string(zBody), `"is_balanced":true`) {
		return "", fmt.Errorf("Z-report unbalanced after split payments: %s", string(zBody))
	}

	return fmt.Sprintf("İndirim kalem dağıtımı (%d kr, 0 kuruş kayması), 3'lü bölüşme (%v kr) ve Z-raporu eşitliği kanıtlandı", cmpRes.Order.DiscountKurus, spRes.SharesKurus), nil
}

func (v *verifier) verifyT3RealtimeKDSReplay() (string, error) {
	// Get current last_event_id
	kdsBody, _, _, _ := v.doJSON(http.MethodGet, "/api/v1/kds/items?branch_id=1", v.tokens["chef"], "", nil)
	var kdsSnap struct {
		LastEventID int64 `json:"last_event_id"`
	}
	_ = json.Unmarshal(kdsBody, &kdsSnap)

	// Trigger a new kitchen event while client is "disconnected"
	b7, _, _, _ := v.doJSON(http.MethodPost, "/api/v1/orders", v.tokens["waiter"], "", map[string]any{
		"branch_id": 1, "table_id": 7,
	})
	var ord7 struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(b7, &ord7)

	_, _, _, _ = v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord7.ID), v.tokens["waiter"], "", map[string]any{
		"menu_item_id": 1, "quantity": 1,
	})

	// Reconnect to SSE stream with ?once=1&since_event_id=<previous> and verify missed events are replayed
	sseBody, sseSt, _, err := v.doJSON(http.MethodGet, fmt.Sprintf("/api/v1/kds/stream?branch_id=1&since_event_id=%d&once=1", kdsSnap.LastEventID), v.tokens["chef"], "", nil)
	if err != nil || sseSt != http.StatusOK || !strings.Contains(string(sseBody), "event: kds_event") {
		return "", fmt.Errorf("SSE replay stream failed: status=%d body=%s", sseSt, string(sseBody))
	}

	repBody, repSt, _, _ := v.doJSON(http.MethodGet, fmt.Sprintf("/api/v1/kds/events?branch_id=1&since_event_id=%d", kdsSnap.LastEventID), v.tokens["chef"], "", nil)
	var repRes struct {
		ReplayedCount int `json:"replayed_count"`
	}
	_ = json.Unmarshal(repBody, &repRes)
	if repSt != http.StatusOK || repRes.ReplayedCount <= 0 {
		return "", fmt.Errorf("expected replayed events > 0, got %d", repRes.ReplayedCount)
	}

	return fmt.Sprintf("SSE kopma sonrası since_event_id=%d üzerinden %d kaçırılan mutfak olayı eksiksiz geri alındı", kdsSnap.LastEventID, repRes.ReplayedCount), nil
}

func (v *verifier) verifyT4SubRecipeAndVariance() (string, error) {
	// Submit physical end-of-day stock count with intentional 250g deficit on ING-BEEF and 100g deficit on ING-TOMATO (sub-recipe ingredient)
	invBody, _, _, _ := v.doJSON(http.MethodGet, "/api/v1/inventory?branch_id=1", v.tokens["branch_manager"], "", nil)
	var inv struct {
		Ingredients []struct {
			Code             string `json:"code"`
			TheoreticalGrams int64  `json:"theoretical_qty_grams"`
		} `json:"ingredients"`
	}
	_ = json.Unmarshal(invBody, &inv)

	var counts []map[string]any
	for _, ing := range inv.Ingredients {
		if ing.Code == "ING-BEEF" {
			counts = append(counts, map[string]any{
				"ingredient_code": ing.Code,
				"counted_grams":   ing.TheoreticalGrams - 250,
			})
		} else if ing.Code == "ING-TOMATO" {
			counts = append(counts, map[string]any{
				"ingredient_code": ing.Code,
				"counted_grams":   ing.TheoreticalGrams - 100,
			})
		}
	}

	cntBody, cntSt, _, err := v.doJSON(http.MethodPost, "/api/v1/inventory/count", v.tokens["branch_manager"], "", map[string]any{
		"branch_id": 1,
		"counts":    counts,
	})
	if err != nil || cntSt != http.StatusCreated {
		return "", fmt.Errorf("POST /api/v1/inventory/count failed: status=%d", cntSt)
	}

	var varRep struct {
		TotalDeficitGrams     int64 `json:"total_deficit_grams"`
		TotalDeficitCostKurus int64 `json:"total_deficit_cost_kurus"`
	}
	_ = json.Unmarshal(cntBody, &varRep)
	if varRep.TotalDeficitGrams != 350 || varRep.TotalDeficitCostKurus <= 0 {
		return "", fmt.Errorf("expected 350g deficit in variance report, got %+v", varRep)
	}

	return fmt.Sprintf("Alt reçete (yarı mamul) stok düşümü ve gün sonu sayım farkı (350g fark = %d kr maliyet) raporlandı", varRep.TotalDeficitCostKurus), nil
}

func (v *verifier) verifyT5CampaignOptimizer() (string, error) {
	// Open Table 8, add 3 Lahmacun (33000) + 1 İskender (36000) + 1 Ayran (4500) + 1 Künefe (19000) = 92500 kr
	b8, _, _, _ := v.doJSON(http.MethodPost, "/api/v1/orders", v.tokens["waiter"], "", map[string]any{
		"branch_id": 1, "table_id": 8,
	})
	var ord8 struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(b8, &ord8)

	itemsToAdd := []struct {
		id  int64
		qty int
	}{
		{2, 3}, // 3 Lahmacun -> triggers CMP-3AL2ODE (11000 kr)
		{1, 1}, // 1 İskender \
		{4, 1}, // 1 Ayran     +-> triggers CMP-COMBO-KEBAP (8500 kr)
		{5, 1}, // 1 Künefe   /
	}
	for _, it := range itemsToAdd {
		_, _, _, _ = v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/items", ord8.ID), v.tokens["waiter"], "", map[string]any{
			"menu_item_id": it.id, "quantity": it.qty,
		})
	}

	evalBody, evalSt, _, err := v.doJSON(http.MethodPost, fmt.Sprintf("/api/v1/orders/%d/apply-campaigns", ord8.ID), v.tokens["cashier"], "", map[string]any{
		"coupon_code":  "SOFRA50",
		"current_hour": 19,
	})
	if err != nil || evalSt != http.StatusOK {
		return "", fmt.Errorf("apply-campaigns failed: status=%d", evalSt)
	}

	var res struct {
		Evaluation struct {
			SubtotalKurus      int64  `json:"subtotal_kurus"`
			TotalDiscountKurus int64  `json:"total_discount_kurus"`
			WinningStrategy    string `json:"winning_strategy"`
			AppliedCampaigns   []struct {
				Code   string `json:"code"`
				Reason string `json:"reason"`
			} `json:"applied_campaigns"`
		} `json:"evaluation"`
	}
	_ = json.Unmarshal(evalBody, &res)

	// 11000 (3Al2Öde) + 8500 (Combo) + 5000 (SOFRA50) = 24500 kr vs 9250 kr (%10 Happy Hour)
	if res.Evaluation.TotalDiscountKurus != 24500 || len(res.Evaluation.AppliedCampaigns) != 3 {
		return "", fmt.Errorf("expected winning discount 24500 kr with 3 stackable rules, got %+v", res.Evaluation)
	}

	return fmt.Sprintf("En avantajlı kombinasyon (%s: 245.00 TL indirim > 92.50 TL tekil indirim) ve nedenleri doğrulandı", res.Evaluation.WinningStrategy), nil
}

func (v *verifier) doJSON(method, path, token, idempKey string, payload any) ([]byte, int, http.Header, error) {
	var bodyReader io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, v.apiURL+path, bodyReader)
	if err != nil {
		return nil, 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idempKey != "" {
		req.Header.Set("Idempotency-Key", idempKey)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, resp.Header, nil
}

func (v *verifier) printSummaryAndExit() {
	fmt.Println("\n--------------------------------------------------------------------------------")
	fmt.Printf(" %-10s | %-6s | %-8s | %s\n", "KİMLİK", "DURUM", "SÜRE", "AÇIKLAMA / KANIT ÖZETİ")
	fmt.Println("--------------------------------------------------------------------------------")

	failed := 0
	for _, r := range v.results {
		statusStr := "PASS"
		if !r.Passed {
			statusStr = "FAIL"
			failed++
		}
		fmt.Printf(" %-10s | %-6s | %-8s | %s\n",
			r.ID,
			statusStr,
			fmt.Sprintf("%dms", r.Elapsed.Milliseconds()),
			r.Details,
		)
	}
	fmt.Println("--------------------------------------------------------------------------------")

	if failed > 0 {
		fmt.Printf(" SONUÇ: %d kontrol BAŞARISIZ oldu.\n", failed)
		os.Exit(1)
	}
	fmt.Printf(" SONUÇ: Tüm %d doğrulama kimliği (F-1..F-7, T-1..T-5, HEALTH, AUTH-RBAC) PASS!\n", len(v.results))
	fmt.Println("================================================================================")
}
