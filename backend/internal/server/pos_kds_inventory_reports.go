package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"

	"github.com/efehidir/ddo/backend/internal/auth"
	"github.com/efehidir/ddo/backend/internal/domain"
)

func (s *POSService) handleListKDSItems(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	branchID := s.resolveBranchID(c, claims)
	station := strings.ToLower(strings.TrimSpace(c.Query("station", "all")))

	query := `
		SELECT
			i.id, i.order_id, o.branch_id, t.code, i.item_name, i.variant_name, i.modifier_names,
			i.quantity, i.station, i.course, i.prep_time_sec, i.kds_status, i.created_at
		FROM "dz-order-items" i
		JOIN "dz-orders" o ON o.id = i.order_id
		JOIN "dz-tables" t ON t.id = o.table_id
		WHERE o.branch_id = $1
		  AND i.is_cancelled = FALSE
		  AND i.kds_status IN ('PENDING', 'PREPARING', 'READY')
		  AND ($2 = 'all' OR i.station = $2)
		ORDER BY i.created_at ASC
	`

	rows, err := s.queries(c.UserContext()).Query(c.UserContext(), query, branchID, station)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer rows.Close()

	var rawItems []domain.KDSTicketItem
	for rows.Next() {
		var it domain.KDSTicketItem
		if err := rows.Scan(
			&it.OrderItemID, &it.OrderID, &it.BranchID, &it.TableCode,
			&it.ItemName, &it.VariantName, &it.ModifierNames,
			&it.Quantity, &it.Station, &it.Course, &it.PrepTimeSec,
			&it.KDSStatus, &it.CreatedAt,
		); err == nil {
			rawItems = append(rawItems, it)
		}
	}

	enriched := domain.EnrichAndSortKDSItems(rawItems, time.Now().UTC())

	var lastEventID int64
	_ = s.queries(c.UserContext()).QueryRow(c.UserContext(), `
		SELECT COALESCE(MAX(id), 0) FROM "dz-order-events" WHERE branch_id = $1
	`, branchID).Scan(&lastEventID)

	delayedCount := 0
	for _, it := range enriched {
		if it.IsDelayed {
			delayedCount++
		}
	}

	return c.JSON(fiber.Map{
		"branch_id":     branchID,
		"station":       station,
		"last_event_id": lastEventID,
		"delayed_count": delayedCount,
		"items":         enriched,
	})
}

func (s *POSService) handleUpdateKDSItemStatus(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	itemID, err := strconv.ParseInt(c.Params("itemId"), 10, 64)
	if err != nil || itemID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_ITEM_ID", "Geçersiz kalem kimliği.")
	}

	var req struct {
		Status string `json:"status"` // "PREPARING", "READY", "SERVED"
	}
	if err := c.BodyParser(&req); err != nil {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON gövdesi.")
	}
	req.Status = strings.ToUpper(strings.TrimSpace(req.Status))
	if req.Status != "PREPARING" && req.Status != "READY" && req.Status != "SERVED" {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_STATUS", "KDS durumu PREPARING, READY veya SERVED olmalıdır.")
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TX_ERROR", err.Error())
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID, branchID int64
	var itemName, station, oldStatus string
	err = tx.QueryRow(ctx, `
		SELECT i.order_id, o.branch_id, i.item_name, i.station, i.kds_status
		FROM "dz-order-items" i
		JOIN "dz-orders" o ON o.id = i.order_id
		WHERE i.id = $1
		FOR UPDATE
	`, itemID).Scan(&orderID, &branchID, &itemName, &station, &oldStatus)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "ITEM_NOT_FOUND", "Mutfak kalemi bulunamadı.")
	}

	_, err = tx.Exec(ctx, `
		UPDATE "dz-order-items"
		SET kds_status = $1, is_produced = TRUE, version = version + 1, updated_at = NOW()
		WHERE id = $2
	`, req.Status, itemID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "UPDATE_ERROR", err.Error())
	}

	evID, err := s.appendOrderEventTx(ctx, tx, branchID, orderID, claims.UserID, domain.EventItemStatus, fiber.Map{
		"order_item_id": itemID,
		"item_name":     itemName,
		"station":       station,
		"old_status":    oldStatus,
		"new_status":    req.Status,
	})
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "EVENT_LOG_ERROR", err.Error())
	}

	_ = s.writeAuditTx(ctx, tx, branchID, claims.UserID, claims.Role, "KDS_STATUS_UPDATED", "order_item", fmt.Sprintf("%d", itemID),
		fiber.Map{"kds_status": oldStatus},
		fiber.Map{"kds_status": req.Status, "is_produced": true},
	)

	if err := tx.Commit(ctx); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "COMMIT_ERROR", err.Error())
	}

	s.broker.Publish(domain.KDSStreamEvent{
		EventID:   evID,
		BranchID:  branchID,
		OrderID:   orderID,
		Station:   station,
		EventType: domain.EventItemStatus,
		Summary:   fmt.Sprintf("%s durumu %s -> %s olarak güncellendi", itemName, oldStatus, req.Status),
		CreatedAt: time.Now().UTC(),
	})

	return c.JSON(fiber.Map{
		"order_item_id": itemID,
		"order_id":      orderID,
		"kds_status":    req.Status,
		"is_produced":   true,
		"event_id":      evID,
	})
}

func (s *POSService) handleReplayKDSEvents(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	branchID := s.resolveBranchID(c, claims)

	sinceID, _ := strconv.ParseInt(c.Query("since_event_id", "0"), 10, 64)
	if lastHeader := c.Get("Last-Event-ID"); lastHeader != "" && sinceID == 0 {
		sinceID, _ = strconv.ParseInt(lastHeader, 10, 64)
	}

	rows, err := s.queries(c.UserContext()).Query(c.UserContext(), `
		SELECT id, branch_id, order_id, event_type, payload, created_at
		FROM "dz-order-events"
		WHERE branch_id = $1 AND id > $2
		ORDER BY id ASC
		LIMIT 200
	`, branchID, sinceID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer rows.Close()

	var events []fiber.Map
	var latestID = sinceID
	for rows.Next() {
		var id, bID, oID int64
		var eType string
		var raw []byte
		var created time.Time
		if err := rows.Scan(&id, &bID, &oID, &eType, &raw, &created); err == nil {
			latestID = id
			var parsed any
			_ = json.Unmarshal(raw, &parsed)
			events = append(events, fiber.Map{
				"event_id":   id,
				"branch_id":  bID,
				"order_id":   oID,
				"event_type": eType,
				"payload":    parsed,
				"created_at": created,
			})
		}
	}
	if events == nil {
		events = []fiber.Map{}
	}

	return c.JSON(fiber.Map{
		"branch_id":      branchID,
		"since_event_id": sinceID,
		"last_event_id":  latestID,
		"replayed_count": len(events),
		"events":         events,
	})
}

func (s *POSService) handleKDSStreamSSE(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	branchID := s.resolveBranchID(c, claims)
	station := strings.ToLower(strings.TrimSpace(c.Query("station", "all")))

	sinceID, _ := strconv.ParseInt(c.Query("since_event_id", "0"), 10, 64)
	if lastHeader := c.Get("Last-Event-ID"); lastHeader != "" && sinceID == 0 {
		sinceID, _ = strconv.ParseInt(lastHeader, 10, 64)
	}

	// Pre-fetch missed events from PostgreSQL "dz-order-events" before streaming
	var initialReplay []domain.KDSStreamEvent
	rows, err := s.queries(c.UserContext()).Query(c.UserContext(), `
		SELECT id, branch_id, order_id, event_type, created_at
		FROM "dz-order-events"
		WHERE branch_id = $1 AND id > $2
		ORDER BY id ASC
		LIMIT 100
	`, branchID, sinceID)
	if err == nil {
		for rows.Next() {
			var ev domain.KDSStreamEvent
			if scanErr := rows.Scan(&ev.EventID, &ev.BranchID, &ev.OrderID, &ev.EventType, &ev.CreatedAt); scanErr == nil {
				ev.Station = station
				ev.Summary = "Replayed event from dz-order-events"
				initialReplay = append(initialReplay, ev)
			}
		}
		rows.Close()
	}

	// If client requested a short burst (e.g. ?once=1 for automated test / curl verification), send replay & close cleanly
	if c.Query("once") == "1" {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		var sb strings.Builder
		for _, ev := range initialReplay {
			b, _ := json.Marshal(ev)
			sb.WriteString(fmt.Sprintf("id: %d\nevent: kds_event\ndata: %s\n\n", ev.EventID, string(b)))
		}
		return c.Status(fiber.StatusOK).SendString(sb.String())
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
		for _, ev := range initialReplay {
			b, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "id: %d\nevent: kds_event\ndata: %s\n\n", ev.EventID, string(b))
		}
		_ = w.Flush()

		ch, unsub := s.broker.Subscribe()
		defer unsub()

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if ev.BranchID == branchID && (station == "all" || ev.Station == "" || ev.Station == station) {
					b, _ := json.Marshal(ev)
					if _, err := fmt.Fprintf(w, "id: %d\nevent: kds_event\ndata: %s\n\n", ev.EventID, string(b)); err != nil {
						return
					}
					if err := w.Flush(); err != nil {
						return
					}
				}
			case t := <-ticker.C:
				if _, err := fmt.Fprintf(w, ": heartbeat %d\n\n", t.Unix()); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			}
		}
	}))

	return nil
}

func (s *POSService) handleGetInventory(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	branchID := s.resolveBranchID(c, claims)
	ctx := c.UserContext()

	ingRows, err := s.queries(ctx).Query(ctx, `
		SELECT id, code, name, unit, is_semi_finished, theoretical_qty_grams, cost_per_unit_kurus, updated_at
		FROM "dz-ingredients"
		WHERE branch_id = $1
		ORDER BY is_semi_finished DESC, id ASC
	`, branchID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer ingRows.Close()

	var ingredients []fiber.Map
	for ingRows.Next() {
		var id, qty, cost int64
		var code, name, unit string
		var isSemi bool
		var updated time.Time
		if err := ingRows.Scan(&id, &code, &name, &unit, &isSemi, &qty, &cost, &updated); err == nil {
			ingredients = append(ingredients, fiber.Map{
				"id":                    id,
				"code":                  code,
				"name":                  name,
				"unit":                  unit,
				"is_semi_finished":      isSemi,
				"theoretical_qty_grams": qty,
				"cost_per_unit_kurus":   cost,
				"updated_at":            updated,
			})
		}
	}

	movRows, err := s.queries(ctx).Query(ctx, `
		SELECT m.id, i.code, i.name, COALESCE(m.order_id, 0), m.movement_type, m.delta_grams, m.cost_kurus, m.reason, m.created_at
		FROM "dz-stock-movements" m
		JOIN "dz-ingredients" i ON i.id = m.ingredient_id
		WHERE m.branch_id = $1
		ORDER BY m.id DESC
		LIMIT 40
	`, branchID)
	var movements []fiber.Map
	if err == nil {
		defer movRows.Close()
		for movRows.Next() {
			var id, orderID, delta, cost int64
			var code, name, mType, reason string
			var created time.Time
			if err := movRows.Scan(&id, &code, &name, &orderID, &mType, &delta, &cost, &reason, &created); err == nil {
				movements = append(movements, fiber.Map{
					"id":              id,
					"ingredient_code": code,
					"ingredient_name": name,
					"order_id":        orderID,
					"movement_type":   mType,
					"delta_grams":     delta,
					"cost_kurus":      cost,
					"reason":          reason,
					"created_at":      created,
				})
			}
		}
	}
	if movements == nil {
		movements = []fiber.Map{}
	}

	return c.JSON(fiber.Map{
		"branch_id":   branchID,
		"ingredients": ingredients,
		"movements":   movements,
	})
}

func (s *POSService) handleSubmitStockCount(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	var req struct {
		BranchID int64 `json:"branch_id"`
		Counts   []struct {
			IngredientCode string `json:"ingredient_code"`
			CountedGrams   int64  `json:"counted_grams"`
		} `json:"counts"`
	}
	if err := c.BodyParser(&req); err != nil {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON gövdesi.")
	}
	if req.BranchID <= 0 {
		req.BranchID = claims.BranchID
		if req.BranchID <= 0 {
			req.BranchID = 1
		}
	}

	countMap := make(map[string]int64)
	if len(req.Counts) == 0 || len(req.Counts) > 100 {
		return auth.RespondError(c, 400, "INVALID_COUNT", "En az bir fiziksel sayım girin.")
	}
	for _, item := range req.Counts {
		code := strings.TrimSpace(item.IngredientCode)
		if code == "" || item.CountedGrams < 0 || item.CountedGrams > 1000000000 {
			return auth.RespondError(c, 400, "INVALID_COUNT", "Sayım miktarı negatif olamaz ve geçerli bir malzeme seçilmelidir.")
		}
		if _, exists := countMap[code]; exists {
			return auth.RespondError(c, 400, "DUPLICATE_COUNT", "Aynı hammadde iki kez sayılamaz.")
		}
		countMap[code] = item.CountedGrams
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TX_ERROR", err.Error())
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ingRows, err := tx.Query(ctx, `
		SELECT id, code, name, unit, theoretical_qty_grams, cost_per_unit_kurus
		FROM "dz-ingredients"
		WHERE branch_id = $1 AND is_semi_finished = FALSE
		ORDER BY id ASC
		FOR UPDATE
	`, req.BranchID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}

	var inputs []domain.StockCountInput
	for ingRows.Next() {
		var in domain.StockCountInput
		if err := ingRows.Scan(&in.IngredientID, &in.IngredientCode, &in.IngredientName, &in.Unit, &in.TheoreticalGrams, &in.CostPerUnitKurus); err == nil {
			if counted, ok := countMap[in.IngredientCode]; ok {
				in.CountedGrams = counted
			} else {
				continue
			}
			inputs = append(inputs, in)
		}
	}
	ingRows.Close()
	if len(inputs) != len(countMap) {
		return auth.RespondError(c, 400, "UNKNOWN_INGREDIENT", "Sayılan hammaddeler bu şubede bulunamadı.")
	}

	report := domain.ComputeStockVarianceReport(inputs)

	for _, row := range report.Rows {
		_, _ = tx.Exec(ctx, `
			INSERT INTO "dz-stock-counts" (
				branch_id, ingredient_id, theoretical_grams, counted_grams, variance_grams, variance_cost_kurus, counted_by
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, req.BranchID, row.IngredientID, row.TheoreticalGrams, row.CountedGrams, row.VarianceGrams, row.VarianceCostKurus, claims.UserID)
	}

	_ = s.writeAuditTx(ctx, tx, req.BranchID, claims.UserID, claims.Role, "STOCK_COUNT_SUBMITTED", "inventory", fmt.Sprintf("branch-%d", req.BranchID), nil, report)

	if err := tx.Commit(ctx); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "COMMIT_ERROR", err.Error())
	}

	return c.Status(fiber.StatusCreated).JSON(report)
}

func (s *POSService) handleGetVarianceReport(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	branchID := s.resolveBranchID(c, claims)
	ctx := c.UserContext()

	// Check if physical counts exist; if not, compute live report with simulated slight physical variance on first raw ingredient
	countRows, err := s.queries(ctx).Query(ctx, `
		SELECT DISTINCT ON (c.ingredient_id)
			i.id, i.code, i.name, i.unit, c.theoretical_grams, c.counted_grams, i.cost_per_unit_kurus
		FROM "dz-stock-counts" c
		JOIN "dz-ingredients" i ON i.id = c.ingredient_id
		WHERE c.branch_id = $1
		ORDER BY c.ingredient_id, c.id DESC
	`, branchID)
	var inputs []domain.StockCountInput
	if err == nil {
		for countRows.Next() {
			var in domain.StockCountInput
			if err := countRows.Scan(&in.IngredientID, &in.IngredientCode, &in.IngredientName, &in.Unit, &in.TheoreticalGrams, &in.CountedGrams, &in.CostPerUnitKurus); err == nil {
				inputs = append(inputs, in)
			}
		}
		countRows.Close()
	}

	if len(inputs) == 0 {
		ingRows, err := s.queries(ctx).Query(ctx, `
			SELECT id, code, name, unit, theoretical_qty_grams, cost_per_unit_kurus
			FROM "dz-ingredients"
			WHERE branch_id = $1 AND is_semi_finished = FALSE
			ORDER BY id ASC
		`, branchID)
		if err != nil {
			return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
		}
		defer ingRows.Close()
		for ingRows.Next() {
			var in domain.StockCountInput
			if err := ingRows.Scan(&in.IngredientID, &in.IngredientCode, &in.IngredientName, &in.Unit, &in.TheoreticalGrams, &in.CostPerUnitKurus); err == nil {
				in.CountedGrams = in.TheoreticalGrams
				inputs = append(inputs, in)
			}
		}
	}

	report := domain.ComputeStockVarianceReport(inputs)
	return c.JSON(report)
}

func (s *POSService) handleOpenShift(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	var req struct {
		BranchID         int64 `json:"branch_id"`
		OpeningCashKurus int64 `json:"opening_cash_kurus"`
	}
	_ = c.BodyParser(&req)
	if req.BranchID <= 0 {
		req.BranchID = s.resolveBranchID(c, claims)
	}
	if req.OpeningCashKurus <= 0 {
		req.OpeningCashKurus = 500000
	}

	var shiftID int64
	err := s.queries(ctx).QueryRow(ctx, `
		INSERT INTO "dz-shifts" (branch_id, opened_by, status, opening_cash_kurus)
		VALUES ($1, $2, 'OPEN', $3)
		RETURNING id
	`, req.BranchID, claims.UserID, req.OpeningCashKurus).Scan(&shiftID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"shift_id":           shiftID,
		"branch_id":          req.BranchID,
		"status":             "OPEN",
		"opening_cash_kurus": req.OpeningCashKurus,
	})
}

func (s *POSService) handleCloseShift(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	var req struct {
		BranchID         int64 `json:"branch_id"`
		ClosingCashKurus int64 `json:"closing_cash_kurus"`
	}
	_ = c.BodyParser(&req)
	if req.BranchID <= 0 {
		req.BranchID = s.resolveBranchID(c, claims)
	}

	var shiftID, openCash, cashPay int64
	err := s.queries(ctx).QueryRow(ctx, `
		SELECT id, opening_cash_kurus, cash_payments_kurus
		FROM "dz-shifts"
		WHERE branch_id = $1 AND status = 'OPEN'
		ORDER BY id DESC LIMIT 1
	`, req.BranchID).Scan(&shiftID, &openCash, &cashPay)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "SHIFT_NOT_FOUND", "Açık vardiya bulunamadı.")
	}

	if req.ClosingCashKurus <= 0 {
		req.ClosingCashKurus = openCash + cashPay
	}

	_, err = s.queries(ctx).Exec(ctx, `
		UPDATE "dz-shifts"
		SET status = 'CLOSED', closed_by = $1, closing_cash_kurus = $2, closed_at = NOW()
		WHERE id = $3
	`, claims.UserID, req.ClosingCashKurus, shiftID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}

	// Open a fresh shift automatically so continuous operations work smoothly
	_, _ = s.queries(ctx).Exec(ctx, `
		INSERT INTO "dz-shifts" (branch_id, opened_by, status, opening_cash_kurus)
		VALUES ($1, $2, 'OPEN', $3)
	`, req.BranchID, claims.UserID, req.ClosingCashKurus)

	return c.JSON(fiber.Map{
		"closed_shift_id":    shiftID,
		"branch_id":          req.BranchID,
		"closing_cash_kurus": req.ClosingCashKurus,
		"status":             "CLOSED",
	})
}

func (s *POSService) handleGetZReport(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	branchID := s.resolveBranchID(c, claims)
	ctx := c.UserContext()

	payRows, err := s.queries(ctx).Query(ctx, `
		SELECT id, order_id, method, split_mode, amount_kurus, tip_kurus
		FROM "dz-payments"
		WHERE branch_id = $1
		ORDER BY id ASC
	`, branchID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer payRows.Close()

	var payments []domain.PaymentRecord
	for payRows.Next() {
		var p domain.PaymentRecord
		if err := payRows.Scan(&p.ID, &p.OrderID, &p.Method, &p.SplitMode, &p.AmountKurus, &p.TipKurus); err == nil {
			payments = append(payments, p)
		}
	}

	var totalPaidAcrossOrders, totalDiscountKurus int64
	_ = s.queries(ctx).QueryRow(ctx, `
		SELECT COALESCE(SUM(paid_kurus), 0), COALESCE(SUM(discount_kurus), 0)
		FROM "dz-orders"
		WHERE branch_id = $1
	`, branchID).Scan(&totalPaidAcrossOrders, &totalDiscountKurus)

	var totalWasteKurus int64
	_ = s.queries(ctx).QueryRow(ctx, `
		SELECT COALESCE(SUM(cost_kurus), 0)
		FROM "dz-stock-movements"
		WHERE branch_id = $1 AND movement_type = 'WASTE'
	`, branchID).Scan(&totalWasteKurus)

	zSummary := domain.ReconcileZReport(payments, totalPaidAcrossOrders)

	var shiftID, openingCash int64
	var shiftStatus string
	_ = s.queries(ctx).QueryRow(ctx, `
		SELECT id, status, opening_cash_kurus
		FROM "dz-shifts"
		WHERE branch_id = $1
		ORDER BY id DESC LIMIT 1
	`, branchID).Scan(&shiftID, &shiftStatus, &openingCash)

	return c.JSON(fiber.Map{
		"branch_id":             branchID,
		"shift_id":              shiftID,
		"shift_status":          shiftStatus,
		"opening_cash_kurus":    openingCash,
		"cash_payments_kurus":   zSummary.CashPaymentsKurus,
		"card_payments_kurus":   zSummary.CardPaymentsKurus,
		"total_payments_kurus":  zSummary.TotalPaymentsKurus,
		"total_tips_kurus":      zSummary.TotalTipsKurus,
		"total_discount_kurus":  totalDiscountKurus,
		"total_waste_kurus":     totalWasteKurus,
		"grand_total_kurus":     zSummary.GrandTotalKurus,
		"payment_count":         zSummary.PaymentCount,
		"orders_paid_sum_kurus": totalPaidAcrossOrders,
		"is_balanced":           zSummary.IsBalanced,
	})
}

func (s *POSService) handleGetHQReport(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	ctx := c.UserContext()

	rows, err := s.queries(ctx).Query(ctx, `
		SELECT
			b.id, b.code, b.name, b.city,
			(SELECT COUNT(*) FROM "dz-tables" t WHERE t.branch_id = b.id AND t.status = 'OCCUPIED') AS occupied_tables,
			(SELECT COUNT(*) FROM "dz-orders" o WHERE o.branch_id = b.id) AS total_orders,
			(SELECT COALESCE(SUM(p.amount_kurus), 0) FROM "dz-payments" p WHERE p.branch_id = b.id) AS revenue_kurus,
			(SELECT COALESCE(SUM(p.tip_kurus), 0) FROM "dz-payments" p WHERE p.branch_id = b.id) AS tips_kurus,
			(SELECT COALESCE(SUM(m.cost_kurus), 0) FROM "dz-stock-movements" m WHERE m.branch_id = b.id AND m.movement_type = 'WASTE') AS waste_kurus
		FROM "dz-branches" b
		ORDER BY b.id ASC
	`)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer rows.Close()

	var branchSummaries []fiber.Map
	var grandRevenue, grandTips, grandWaste, totalOrders, totalOccupied int64

	for rows.Next() {
		var id, occ, ords, rev, tips, waste int64
		var code, name, city string
		if err := rows.Scan(&id, &code, &name, &city, &occ, &ords, &rev, &tips, &waste); err == nil {
			grandRevenue += rev
			grandTips += tips
			grandWaste += waste
			totalOrders += ords
			totalOccupied += occ
			branchSummaries = append(branchSummaries, fiber.Map{
				"branch_id":       id,
				"code":            code,
				"name":            name,
				"city":            city,
				"occupied_tables": occ,
				"total_orders":    ords,
				"revenue_kurus":   rev,
				"tips_kurus":      tips,
				"waste_kurus":     waste,
			})
		}
	}

	return c.JSON(fiber.Map{
		"branch_count":          len(branchSummaries),
		"total_occupied_tables": totalOccupied,
		"total_orders":          totalOrders,
		"grand_revenue_kurus":   grandRevenue,
		"grand_tips_kurus":      grandTips,
		"grand_waste_kurus":     grandWaste,
		"branches":              branchSummaries,
	})
}

func (s *POSService) handleListAuditLogs(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	branchFilter := int64(0)
	if claims.Role != auth.RoleHQManager {
		branchFilter = claims.BranchID
	}
	afterID, _ := strconv.ParseInt(c.Query("after_id", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.Query("limit", "30"))
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	rows, err := s.queries(c.UserContext()).Query(c.UserContext(), `
		SELECT id, COALESCE(branch_id, 0), COALESCE(actor_id, 0), actor_role, action, entity_type, entity_id, old_value, new_value, created_at
		FROM "dz-audit-logs"
		WHERE ($1 = 0 OR id < $1) AND ($3::bigint = 0 OR branch_id = $3)
		ORDER BY id DESC
		LIMIT $2
	`, afterID, limit, branchFilter)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer rows.Close()

	var logs []fiber.Map
	var nextCursor int64
	for rows.Next() {
		var id, bID, actorID int64
		var role, action, eType, eID string
		var oldRaw, newRaw []byte
		var created time.Time
		if err := rows.Scan(&id, &bID, &actorID, &role, &action, &eType, &eID, &oldRaw, &newRaw, &created); err == nil {
			nextCursor = id
			var oldVal, newVal any
			_ = json.Unmarshal(oldRaw, &oldVal)
			_ = json.Unmarshal(newRaw, &newVal)
			logs = append(logs, fiber.Map{
				"id":          id,
				"branch_id":   bID,
				"actor_id":    actorID,
				"actor_role":  role,
				"action":      action,
				"entity_type": eType,
				"entity_id":   eID,
				"old_value":   oldVal,
				"new_value":   newVal,
				"created_at":  created,
			})
		}
	}
	if logs == nil {
		logs = []fiber.Map{}
	}

	return c.JSON(fiber.Map{
		"audit_logs":  logs,
		"next_cursor": nextCursor,
	})
}

func (s *POSService) handleResetDemo(c *fiber.Ctx) error {
	if s.pg == nil {
		return auth.RespondError(c, fiber.StatusServiceUnavailable, "DB_UNAVAILABLE", "Veritabanı bağlantısı aktif değil.")
	}
	if err := s.pg.ResetDemoData(c.UserContext()); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "RESET_FAILED", err.Error())
	}
	return c.JSON(fiber.Map{
		"status":  "reset_complete",
		"message": "Veritabanı ilk deterministik seed durumuna döndürüldü.",
	})
}
