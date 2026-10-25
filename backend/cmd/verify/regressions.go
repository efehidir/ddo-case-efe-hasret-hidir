package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type orderSnapshot struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Version   int64  `json:"version"`
	Total     int64  `json:"total_kurus"`
	Paid      int64  `json:"paid_kurus"`
	Remaining int64  `json:"remaining_kurus"`
	Items     []struct {
		ID   int64 `json:"id"`
		Paid int64 `json:"paid_kurus"`
		Net  int64 `json:"net_total_kurus"`
	} `json:"items"`
}

func (v *verifier) expect(method, path, role, key string, payload any, statuses ...int) ([]byte, error) {
	body, status, _, err := v.doJSON(method, path, v.tokens[role], key, payload)
	if err != nil {
		return nil, err
	}
	for _, expected := range statuses {
		if status == expected {
			return body, nil
		}
	}
	return nil, fmt.Errorf("%s %s: status=%d, expected=%v body=%s", method, path, status, statuses, body)
}

func (v *verifier) open(branch, table int64, role string) (orderSnapshot, error) {
	body, err := v.expect("POST", "/api/v1/orders", role, "", map[string]any{"branch_id": branch, "table_id": table}, 200, 201)
	var order orderSnapshot
	if err == nil {
		err = json.Unmarshal(body, &order)
	}
	return order, err
}

func (v *verifier) verifyFrontend() (string, error) {
	for _, path := range []string{"/health", "/"} {
		resp, err := v.client.Get(v.frontendURL + path)
		if err != nil {
			return "", err
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("frontend %s: %d", path, resp.StatusCode)
		}
	}
	return "Frontend /health ve sunucuda oluşturulan ana sayfa 200", nil
}

func (v *verifier) verifySecurityRegression() (string, error) {
	for _, path := range []string{"/tables", "/orders", "/kds/items", "/kds/events", "/inventory", "/reports/z-report"} {
		role := "branch_manager"
		if _, err := v.expect("GET", "/api/v1"+path+"?branch_id=2", role, "", nil, 403); err != nil {
			return "", err
		}
	}
	order, err := v.open(2, 11, "hq_manager")
	if err != nil {
		return "", err
	}
	for _, suffix := range []string{"", "/rebuild"} {
		if _, err := v.expect("GET", fmt.Sprintf("/api/v1/orders/%d%s", order.ID, suffix), "waiter", "", nil, 403); err != nil {
			return "", err
		}
	}
	if _, err := v.expect("POST", fmt.Sprintf("/api/v1/orders/%d/items", order.ID), "waiter", "", map[string]any{"menu_item_id": 1, "quantity": 1}, 403); err != nil {
		return "", err
	}
	if _, err := v.expect("POST", fmt.Sprintf("/api/v1/orders/%d/items", order.ID), "customer", "", map[string]any{"menu_item_id": 1, "quantity": 1}, 403); err != nil {
		return "", err
	}
	for role, status := range map[string]int{"": 401, "waiter": 403, "branch_manager": 403} {
		if _, err := v.expect("POST", "/api/v1/admin/reset-demo", role, "", nil, status); err != nil {
			return "", err
		}
	}
	if _, err := v.expect("GET", "/api/v1/reports/hq", "branch_manager", "", nil, 403); err != nil {
		return "", err
	}
	if _, err := v.expect("GET", "/api/v1/db/check", "customer", "", nil, 403); err != nil {
		return "", err
	}
	return "Şube 2 okumaları/yazmaları, müşterinin adisyona yazması ve yetkisiz sıfırlama engellendi", nil
}

func (v *verifier) verifyIdempotencyRegression() (string, error) {
	order, err := v.open(1, 10, "waiter")
	if err != nil {
		return "", err
	}
	path := fmt.Sprintf("/api/v1/orders/%d/items", order.ID)
	payload := map[string]any{"menu_item_id": 4, "quantity": 1}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := v.expect("POST", path, "waiter", "concurrent-add", payload, 201)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return "", err
		}
	}
	body, err := v.expect("GET", fmt.Sprintf("/api/v1/orders/%d", order.ID), "waiter", "", nil, 200)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(body, &order); err != nil {
		return "", err
	}
	if len(order.Items) != 1 || order.Total != 4500 {
		return "", fmt.Errorf("duplicate effects: %s", body)
	}
	if _, err := v.expect("POST", path, "waiter", "concurrent-add", map[string]any{"menu_item_id": 4, "quantity": 2}, 409); err != nil {
		return "", err
	}
	_, status, headers, err := v.doJSON("POST", path, v.tokens["cashier"], "concurrent-add", payload)
	if err != nil || status != 201 || headers.Get("X-Idempotent-Replay") != "" {
		return "", fmt.Errorf("user scope: status=%d err=%v", status, err)
	}
	return "8 eşzamanlı aynı anahtar yalnızca 1 kalem oluşturdu; farklı gövde 409; farklı kullanıcı ayrı işlem", nil
}

func (v *verifier) verifyPaymentRegression() (string, error) {
	order, err := v.open(1, 9, "waiter")
	if err != nil {
		return "", err
	}
	base := fmt.Sprintf("/api/v1/orders/%d", order.ID)
	for _, payload := range []map[string]any{{"menu_item_id": 1, "quantity": 1, "variant_id": 11}, {"menu_item_id": 1, "quantity": 1, "modifier_ids": []int64{7}}, {"menu_item_id": 1, "quantity": 1, "modifier_ids": []int64{1, 1}}} {
		if _, err := v.expect("POST", base+"/items", "waiter", "", payload, 400); err != nil {
			return "", err
		}
	}
	body, err := v.expect("POST", base+"/items", "waiter", "", map[string]any{"menu_item_id": 2, "quantity": 1}, 201)
	if err != nil {
		return "", err
	}
	json.Unmarshal(body, &order)
	item := order.Items[0].ID
	if _, err := v.expect("POST", base+"/payments", "cashier", "", map[string]any{"method": "CASH", "split_mode": "BY_ITEM", "selected_item_ids": []int64{item}, "amount_kurus": 1}, 400); err != nil {
		return "", err
	}
	if _, err := v.expect("POST", base+"/payments", "cashier", "", map[string]any{"method": "INVALID", "amount_kurus": 1}, 400); err != nil {
		return "", err
	}
	if _, err := v.expect("POST", base+"/payments", "cashier", "", map[string]any{"method": "CASH", "split_mode": "CUSTOM", "amount_kurus": 3001}, 201); err != nil {
		return "", err
	}
	if _, err := v.expect("POST", base+"/apply-campaigns", "cashier", "", map[string]any{"coupon_code": "SOFRA50"}, 409); err != nil {
		return "", err
	}
	if _, err := v.expect("POST", fmt.Sprintf("%s/items/%d/cancel", base, item), "waiter", "", map[string]any{"produce_waste": true}, 409); err != nil {
		return "", err
	}
	body, err = v.expect("POST", base+"/payments", "cashier", "final-item-payment", map[string]any{"method": "CARD", "split_mode": "BY_ITEM", "selected_item_ids": []int64{item}}, 201)
	if err != nil {
		return "", err
	}
	json.Unmarshal(body, &order)
	if order.Paid != 11000 || order.Items[0].Paid != 11000 || order.Status != "PAID" {
		return "", fmt.Errorf("item/order paid drift: %s", body)
	}
	if _, err := v.expect("POST", base+"/items", "waiter", "", map[string]any{"menu_item_id": 4, "quantity": 1}, 409); err != nil {
		return "", err
	}
	_, status, hdr, err := v.doJSON("POST", base+"/payments", v.tokens["cashier"], "final-item-payment", map[string]any{"method": "CARD", "split_mode": "BY_ITEM", "selected_item_ids": []int64{item}})
	if err != nil || status != 201 || hdr.Get("X-Idempotent-Replay") != "true" {
		return "", fmt.Errorf("payment replay failed: %d %v", status, err)
	}
	return "Kısmi + kalem ödemesi 11000 kuruşu hem kalemde hem adisyonda kapattı; ödeme tekrarı ve kapalı adisyon korundu", nil
}

func (v *verifier) inventory(role string) (map[string]int64, error) {
	body, err := v.expect("GET", "/api/v1/inventory?branch_id=1", role, "", nil, 200)
	if err != nil {
		return nil, err
	}
	var result struct {
		Ingredients []struct {
			Code     string `json:"code"`
			Quantity int64  `json:"theoretical_qty_grams"`
		} `json:"ingredients"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	quantities := map[string]int64{}
	for _, ing := range result.Ingredients {
		quantities[ing.Code] = ing.Quantity
	}
	return quantities, nil
}

func (v *verifier) verifyStockRegression() (string, error) {
	order, err := v.open(1, 9, "waiter")
	if err != nil {
		return "", err
	}
	base := fmt.Sprintf("/api/v1/orders/%d", order.ID)
	before, err := v.inventory("branch_manager")
	if err != nil {
		return "", err
	}
	body, err := v.expect("POST", base+"/items", "waiter", "", map[string]any{"menu_item_id": 1, "quantity": 1, "variant_id": 2, "modifier_ids": []int64{1}}, 201)
	if err != nil {
		return "", err
	}
	json.Unmarshal(body, &order)
	item := order.Items[len(order.Items)-1].ID
	after, err := v.inventory("branch_manager")
	if err != nil {
		return "", err
	}
	for code, delta := range map[string]int64{"ING-BEEF": 270, "ING-TOMATO": 180, "ING-BUTTER": 75, "ING-YOGURT": 120} {
		if before[code]-after[code] != delta {
			return "", fmt.Errorf("recipe %s expected %d consumed %d", code, delta, before[code]-after[code])
		}
	}
	if _, err := v.expect("POST", fmt.Sprintf("%s/items/%d/cancel", base, item), "waiter", "", map[string]any{"produce_waste": false}, 200); err != nil {
		return "", err
	}
	restored, err := v.inventory("branch_manager")
	if err != nil {
		return "", err
	}
	for code, qty := range before {
		if restored[code] != qty {
			return "", fmt.Errorf("stock restoration mismatch %s", code)
		}
	}
	body, err = v.expect("POST", base+"/items", "waiter", "", map[string]any{"menu_item_id": 1, "quantity": 1, "variant_id": 2, "modifier_ids": []int64{1}}, 201)
	if err != nil {
		return "", err
	}
	json.Unmarshal(body, &order)
	item = order.Items[len(order.Items)-1].ID
	if _, err := v.expect("PATCH", fmt.Sprintf("/api/v1/kds/items/%d/status", item), "chef", "", map[string]any{"status": "PREPARING"}, 200); err != nil {
		return "", err
	}
	if _, err := v.expect("POST", fmt.Sprintf("%s/items/%d/cancel", base, item), "waiter", "", map[string]any{"produce_waste": true}, 200); err != nil {
		return "", err
	}
	wasted, err := v.inventory("branch_manager")
	if err != nil {
		return "", err
	}
	for code, qty := range after {
		if wasted[code] != qty {
			return "", fmt.Errorf("waste deducted stock twice %s", code)
		}
	}
	body, err = v.expect("GET", "/api/v1/inventory?branch_id=1", "branch_manager", "", nil, 200)
	if err != nil {
		return "", err
	}
	var movements struct {
		Movements []struct {
			Order int64  `json:"order_id"`
			Type  string `json:"movement_type"`
			Cost  int64  `json:"cost_kurus"`
		} `json:"movements"`
	}
	json.Unmarshal(body, &movements)
	var cost int64
	for _, m := range movements.Movements {
		if m.Order == order.ID && m.Type == "WASTE" {
			cost += m.Cost
		}
	}
	if cost != 24750 {
		return "", fmt.Errorf("expected actual recipe waste cost 24750, got %d", cost)
	}
	if _, err := v.expect("POST", "/api/v1/inventory/count", "branch_manager", "", map[string]any{"counts": []map[string]any{{"ingredient_code": "ING-BEEF", "counted_grams": -1}}}, 400); err != nil {
		return "", err
	}
	return "270g et + 180g domates + 75g tereyağı + 120g yoğurt; tam iptal iadesi ve 24750 kuruş gerçek fire maliyeti", nil
}

func (v *verifier) verifyLargeReplay() (string, error) {
	order, err := v.open(8, 71, "hq_manager")
	if err != nil {
		return "", err
	}
	body, err := v.expect("GET", "/api/v1/kds/items?branch_id=8", "hq_manager", "", nil, 200)
	if err != nil {
		return "", err
	}
	var snapshot struct {
		Last int64 `json:"last_event_id"`
	}
	json.Unmarshal(body, &snapshot)
	for i := 0; i < 205; i++ {
		target := int64(72)
		if i%2 == 1 {
			target = 71
		}
		if _, err := v.expect("POST", fmt.Sprintf("/api/v1/orders/%d/move", order.ID), "hq_manager", "", map[string]any{"target_table_id": target}, 200); err != nil {
			return "", err
		}
	}
	body, err = v.expect("GET", fmt.Sprintf("/api/v1/kds/stream?branch_id=8&since_event_id=%d&once=1", snapshot.Last), "hq_manager", "", nil, 200)
	if err != nil {
		return "", err
	}
	if count := strings.Count(string(body), "event: kds_event"); count != 205 {
		return "", fmt.Errorf("replay lost events: expected 205 got %d", count)
	}
	return "205 kalıcı olay tek SSE yeniden bağlantısında eksiksiz geri alındı", nil
}

func (v *verifier) verifyLiveSSE() (string, error) {
	body, err := v.expect("GET", "/api/v1/kds/items?branch_id=8", "hq_manager", "", nil, 200)
	if err != nil {
		return "", err
	}
	var snapshot struct {
		Last int64 `json:"last_event_id"`
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/v1/kds/stream?branch_id=8&since_event_id=0", v.apiURL), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+v.tokens["hq_manager"])
	req.Header.Set("Last-Event-ID", strconv.FormatInt(snapshot.Last, 10))
	response, err := v.client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", fmt.Errorf("live SSE status %d", response.StatusCode)
	}
	// The stream is already connected before a new committed event is produced.
	if _, err := v.open(8, 73, "hq_manager"); err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			id, err := strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			if err != nil || id <= snapshot.Last {
				return "", fmt.Errorf("Last-Event-ID was ignored: %q", line)
			}
			return "Açık SSE bağlantısı yeni olayı aldı; Last-Event-ID sorgu parametresine üstün geldi", nil
		}
	}
	return "", fmt.Errorf("live SSE did not receive a new event: %v", scanner.Err())
}

func (v *verifier) verifyReadSnapshot() (string, error) {
	order, err := v.open(3, 21, "hq_manager")
	if err != nil {
		return "", err
	}
	var workers sync.WaitGroup
	failures := make(chan error, 2)
	workers.Add(2)
	go func() {
		defer workers.Done()
		for i := 0; i < 20; i++ {
			if _, err := v.expect("POST", fmt.Sprintf("/api/v1/orders/%d/items", order.ID), "hq_manager", "", map[string]any{"menu_item_id": 4, "quantity": 1}, 201); err != nil {
				failures <- err
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 35; i++ {
			body, err := v.expect("GET", fmt.Sprintf("/api/v1/orders/%d/rebuild", order.ID), "hq_manager", "", nil, 200)
			if err != nil {
				failures <- err
				return
			}
			var proof struct {
				Consistent bool `json:"is_consistent"`
			}
			if err := json.Unmarshal(body, &proof); err != nil || !proof.Consistent {
				failures <- fmt.Errorf("mixed read snapshot: %s", body)
				return
			}
		}
	}()
	workers.Wait()
	close(failures)
	for err := range failures {
		return "", err
	}
	return "20 eşzamanlı kalem yazımı sırasında 35 rebuild okuması aynı MVCC anlık görüntüsünden tutarlı döndü", nil
}

func (v *verifier) verifyShiftRegression() (string, error) {
	body, err := v.expect("GET", "/api/v1/reports/z-report?branch_id=1", "cashier", "", nil, 200)
	if err != nil {
		return "", err
	}
	var old struct {
		ID    int64 `json:"shift_id"`
		Total int64 `json:"total_payments_kurus"`
	}
	json.Unmarshal(body, &old)
	if _, err := v.expect("POST", "/api/v1/shifts/open", "cashier", "", map[string]any{"branch_id": 1}, 409); err != nil {
		return "", err
	}
	if _, err := v.expect("POST", "/api/v1/shifts/close", "cashier", "close-shift", map[string]any{"branch_id": 1, "closing_cash_kurus": 0}, 200); err != nil {
		return "", err
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, status, _, _ := v.doJSON("POST", "/api/v1/shifts/open", v.tokens["cashier"], "", map[string]any{"branch_id": 1, "opening_cash_kurus": 0})
			statuses <- status
		}()
	}
	wg.Wait()
	close(statuses)
	created, conflicts := 0, 0
	for status := range statuses {
		if status == 201 {
			created++
		}
		if status == 409 {
			conflicts++
		}
	}
	if created != 1 || conflicts != 1 {
		return "", fmt.Errorf("shift race: created=%d conflicts=%d", created, conflicts)
	}
	body, err = v.expect("GET", "/api/v1/reports/z-report?branch_id=1", "cashier", "", nil, 200)
	if err != nil {
		return "", err
	}
	var fresh struct {
		Total int64 `json:"total_payments_kurus"`
	}
	json.Unmarshal(body, &fresh)
	if fresh.Total != 0 {
		return "", fmt.Errorf("new shift includes previous payments: %s", body)
	}
	body, err = v.expect("GET", fmt.Sprintf("/api/v1/reports/z-report?branch_id=1&shift_id=%d", old.ID), "cashier", "", nil, 200)
	if err != nil {
		return "", err
	}
	var historical struct {
		Total int64 `json:"total_payments_kurus"`
	}
	json.Unmarshal(body, &historical)
	if historical.Total != old.Total {
		return "", fmt.Errorf("historical report changed: %s", body)
	}
	return "Aynı şubede yalnızca 1 açık vardiya; eşzamanlı açılış 201/409; eski rapor korundu, yeni vardiya sıfırdan başladı", nil
}
