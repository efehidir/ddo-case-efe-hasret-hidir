package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"

	"github.com/efehidir/ddo/backend/internal/auth"
	"github.com/efehidir/ddo/backend/internal/domain"
)

func (s *POSService) resolveBranchID(c *fiber.Ctx, claims auth.Claims) int64 {
	if q := c.Query("branch_id"); q != "" {
		if b, err := strconv.ParseInt(q, 10, 64); err == nil && b > 0 {
			return b
		}
	}
	if claims.BranchID > 0 {
		return claims.BranchID
	}
	return 1
}

func (s *POSService) handleListTables(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	branchID := s.resolveBranchID(c, claims)

	rows, err := s.queries(c.UserContext()).Query(c.UserContext(), `
		SELECT
			t.id, t.branch_id, t.code, t.name, t.capacity, t.status,
			COALESCE(t.active_order_id, 0), COALESCE(t.merged_into_table_id, 0), t.version,
			COALESCE(o.total_kurus, 0), COALESCE(o.paid_kurus, 0), COALESCE(o.version, 0)
		FROM "dz-tables" t
		LEFT JOIN "dz-orders" o ON o.id = t.active_order_id
		WHERE t.branch_id = $1
		ORDER BY t.id ASC
	`, branchID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer rows.Close()

	var tables []fiber.Map
	for rows.Next() {
		var id, bID, activeOrderID, mergedIntoID, ver, totalKurus, paidKurus, orderVer int64
		var code, name, status string
		var capInt int
		if err := rows.Scan(&id, &bID, &code, &name, &capInt, &status, &activeOrderID, &mergedIntoID, &ver, &totalKurus, &paidKurus, &orderVer); err == nil {
			tables = append(tables, fiber.Map{
				"id":                   id,
				"branch_id":            bID,
				"code":                 code,
				"name":                 name,
				"capacity":             capInt,
				"status":               status,
				"active_order_id":      activeOrderID,
				"merged_into_table_id": mergedIntoID,
				"version":              ver,
				"order_total_kurus":    totalKurus,
				"order_paid_kurus":     paidKurus,
				"order_version":        orderVer,
			})
		}
	}

	return c.JSON(fiber.Map{
		"branch_id": branchID,
		"tables":    tables,
	})
}

func (s *POSService) handleListOrders(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	branchID := s.resolveBranchID(c, claims)

	afterID, _ := strconv.ParseInt(c.Query("after_id", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.Query("limit", "25"))
	if limit <= 0 || limit > 100 {
		limit = 25
	}

	query := `
		SELECT o.id, o.branch_id, o.table_id, t.code, o.status, o.version,
		       o.subtotal_kurus, o.discount_kurus, o.total_kurus, o.paid_kurus, o.tip_kurus, o.coupon_code, o.created_at
		FROM "dz-orders" o
		JOIN "dz-tables" t ON t.id = o.table_id
		WHERE o.branch_id = $1 AND ($2 = 0 OR o.id < $2)
		ORDER BY o.id DESC
		LIMIT $3
	`
	rows, err := s.queries(c.UserContext()).Query(c.UserContext(), query, branchID, afterID, limit)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer rows.Close()

	var orders []fiber.Map
	var nextCursor int64
	for rows.Next() {
		var id, bID, tableID, ver, sub, disc, tot, paid, tip int64
		var tableCode, status, coupon string
		var createdAt time.Time
		if err := rows.Scan(&id, &bID, &tableID, &tableCode, &status, &ver, &sub, &disc, &tot, &paid, &tip, &coupon, &createdAt); err == nil {
			nextCursor = id
			orders = append(orders, fiber.Map{
				"id":              id,
				"branch_id":       bID,
				"table_id":        tableID,
				"table_code":      tableCode,
				"status":          status,
				"version":         ver,
				"subtotal_kurus":  sub,
				"discount_kurus":  disc,
				"total_kurus":     tot,
				"paid_kurus":      paid,
				"remaining_kurus": max64(0, tot-paid),
				"tip_kurus":       tip,
				"coupon_code":     coupon,
				"created_at":      createdAt,
			})
		}
	}

	return c.JSON(fiber.Map{
		"orders":      orders,
		"next_cursor": nextCursor,
	})
}

func (s *POSService) handleCreateOrder(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	idempKey, replayed := s.checkIdempotency(ctx, c, claims.UserID)
	if replayed {
		return nil
	}

	var req struct {
		BranchID int64 `json:"branch_id"`
		TableID  int64 `json:"table_id"`
	}
	if err := c.BodyParser(&req); err != nil || req.TableID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "Geçerli bir table_id zorunludur.")
	}
	if req.BranchID <= 0 {
		req.BranchID = claims.BranchID
		if req.BranchID <= 0 {
			req.BranchID = 1
		}
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TX_ERROR", err.Error())
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tableBranchID int64
	var tableStatus string
	var existingOrderID *int64
	err = tx.QueryRow(ctx, `
		SELECT branch_id, status, active_order_id
		FROM "dz-tables"
		WHERE id = $1
		FOR UPDATE
	`, req.TableID).Scan(&tableBranchID, &tableStatus, &existingOrderID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "TABLE_NOT_FOUND", "Masa bulunamadı.")
	}

	if existingOrderID != nil && *existingOrderID > 0 && tableStatus == "OCCUPIED" {
		_ = tx.Commit(ctx)
		orderDetail, err := s.loadOrderDetail(ctx, *existingOrderID)
		if err != nil {
			return auth.RespondError(c, fiber.StatusInternalServerError, "ORDER_LOAD_ERROR", err.Error())
		}
		return s.saveIdempotencyAndRespond(ctx, c, idempKey, claims.UserID, fiber.StatusOK, orderDetail)
	}

	var orderID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO "dz-orders" (branch_id, table_id, status, version, opened_by)
		VALUES ($1, $2, 'OPEN', 1, $3)
		RETURNING id
	`, tableBranchID, req.TableID, claims.UserID).Scan(&orderID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "ORDER_CREATE_ERROR", err.Error())
	}

	_, err = tx.Exec(ctx, `
		UPDATE "dz-tables"
		SET status = 'OCCUPIED', active_order_id = $1, merged_into_table_id = NULL, version = version + 1, updated_at = NOW()
		WHERE id = $2
	`, orderID, req.TableID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TABLE_UPDATE_ERROR", err.Error())
	}

	_, err = s.appendOrderEventTx(ctx, tx, tableBranchID, orderID, claims.UserID, domain.EventOrderOpened, fiber.Map{
		"table_id":  req.TableID,
		"opened_by": claims.UserID,
	})
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "EVENT_LOG_ERROR", err.Error())
	}

	_ = s.writeAuditTx(ctx, tx, tableBranchID, claims.UserID, claims.Role, "ORDER_OPENED", "order", fmt.Sprintf("%d", orderID), nil, fiber.Map{"table_id": req.TableID})

	if err := tx.Commit(ctx); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "COMMIT_ERROR", err.Error())
	}

	orderDetail, _ := s.loadOrderDetail(ctx, orderID)
	return s.saveIdempotencyAndRespond(ctx, c, idempKey, claims.UserID, fiber.StatusCreated, orderDetail)
}

func (s *POSService) loadOrderDetail(ctx context.Context, orderID int64) (fiber.Map, error) {
	var (
		id, branchID, tableID, version, sub, disc, tot, paid, tip int64
		tableCode, status, coupon                                 string
		campaignExpRaw                                            []byte
		createdAt, updatedAt                                      time.Time
	)

	err := s.queries(ctx).QueryRow(ctx, `
		SELECT o.id, o.branch_id, o.table_id, t.code, o.status, o.version,
		       o.subtotal_kurus, o.discount_kurus, o.total_kurus, o.paid_kurus, o.tip_kurus,
		       o.coupon_code, o.campaign_explanation, o.created_at, o.updated_at
		FROM "dz-orders" o
		JOIN "dz-tables" t ON t.id = o.table_id
		WHERE o.id = $1
	`, orderID).Scan(
		&id, &branchID, &tableID, &tableCode, &status, &version,
		&sub, &disc, &tot, &paid, &tip,
		&coupon, &campaignExpRaw, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	var campaignDecisions []domain.CampaignDecision
	_ = json.Unmarshal(campaignExpRaw, &campaignDecisions)
	if campaignDecisions == nil {
		campaignDecisions = []domain.CampaignDecision{}
	}

	itemRows, err := s.queries(ctx).Query(ctx, `
		SELECT id, menu_item_id, item_name, COALESCE(variant_id, 0), variant_name, modifier_names,
		       quantity, unit_price_kurus, line_subtotal_kurus, allocated_discount_kurus,
		       net_total_kurus, paid_kurus, station, course, prep_time_sec, kds_status,
		       is_produced, is_cancelled, cancel_reason, created_at
		FROM "dz-order-items"
		WHERE order_id = $1
		ORDER BY id ASC
	`, orderID)
	if err != nil {
		return nil, err
	}
	defer itemRows.Close()

	var items []fiber.Map
	for itemRows.Next() {
		var itemID, menuItemID, variantID, unitPrice, lineSub, allocDisc, netTot, itemPaid int64
		var itemName, variantName, modNames, station, kdsStatus, cancelReason string
		var qty, course, prepSec int
		var isProd, isCanc bool
		var itemCreated time.Time
		if err := itemRows.Scan(
			&itemID, &menuItemID, &itemName, &variantID, &variantName, &modNames,
			&qty, &unitPrice, &lineSub, &allocDisc, &netTot, &itemPaid,
			&station, &course, &prepSec, &kdsStatus, &isProd, &isCanc, &cancelReason, &itemCreated,
		); err == nil {
			items = append(items, fiber.Map{
				"id":                       itemID,
				"menu_item_id":             menuItemID,
				"item_name":                itemName,
				"variant_id":               variantID,
				"variant_name":             variantName,
				"modifier_names":           modNames,
				"quantity":                 qty,
				"unit_price_kurus":         unitPrice,
				"line_subtotal_kurus":      lineSub,
				"allocated_discount_kurus": allocDisc,
				"net_total_kurus":          netTot,
				"paid_kurus":               itemPaid,
				"remaining_kurus":          max64(0, netTot-itemPaid),
				"station":                  station,
				"course":                   course,
				"prep_time_sec":            prepSec,
				"kds_status":               kdsStatus,
				"is_produced":              isProd,
				"is_cancelled":             isCanc,
				"cancel_reason":            cancelReason,
				"created_at":               itemCreated,
			})
		}
	}
	if items == nil {
		items = []fiber.Map{}
	}

	payRows, err := s.queries(ctx).Query(ctx, `
		SELECT id, method, split_mode, amount_kurus, tip_kurus, idempotency_key, created_at
		FROM "dz-payments"
		WHERE order_id = $1
		ORDER BY id ASC
	`, orderID)
	var payments []fiber.Map
	if err == nil {
		defer payRows.Close()
		for payRows.Next() {
			var pID, amt, pTip int64
			var method, splitMode, idemp string
			var pCreated time.Time
			if err := payRows.Scan(&pID, &method, &splitMode, &amt, &pTip, &idemp, &pCreated); err == nil {
				payments = append(payments, fiber.Map{
					"id":              pID,
					"method":          method,
					"split_mode":      splitMode,
					"amount_kurus":    amt,
					"tip_kurus":       pTip,
					"idempotency_key": idemp,
					"created_at":      pCreated,
				})
			}
		}
	}
	if payments == nil {
		payments = []fiber.Map{}
	}

	return fiber.Map{
		"id":                   id,
		"branch_id":            branchID,
		"table_id":             tableID,
		"table_code":           tableCode,
		"status":               status,
		"version":              version,
		"subtotal_kurus":       sub,
		"discount_kurus":       disc,
		"total_kurus":          tot,
		"paid_kurus":           paid,
		"remaining_kurus":      max64(0, tot-paid),
		"tip_kurus":            tip,
		"coupon_code":          coupon,
		"campaign_explanation": campaignDecisions,
		"items":                items,
		"payments":             payments,
		"created_at":           createdAt,
		"updated_at":           updatedAt,
	}, nil
}

func (s *POSService) handleGetOrder(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	orderID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || orderID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_ORDER_ID", "Geçersiz adisyon kimliği.")
	}

	detail, err := s.loadOrderDetail(c.UserContext(), orderID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "ORDER_NOT_FOUND", "Adisyon bulunamadı.")
	}
	return c.JSON(detail)
}

func (s *POSService) handleRebuildOrderFromEvents(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	ctx := c.UserContext()
	orderID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || orderID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_ORDER_ID", "Geçersiz adisyon kimliği.")
	}

	var dbSub, dbDisc, dbTot, dbPaid, dbTip, dbVer int64
	var dbStatus string
	err = s.queries(ctx).QueryRow(ctx, `
		SELECT subtotal_kurus, discount_kurus, total_kurus, paid_kurus, tip_kurus, version, status
		FROM "dz-orders"
		WHERE id = $1
	`, orderID).Scan(&dbSub, &dbDisc, &dbTot, &dbPaid, &dbTip, &dbVer, &dbStatus)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "ORDER_NOT_FOUND", "Adisyon bulunamadı.")
	}

	rows, err := s.queries(ctx).Query(ctx, `
		SELECT id, branch_id, order_id, event_type, COALESCE(actor_id, 0), payload, created_at
		FROM "dz-order-events"
		WHERE order_id = $1
		ORDER BY id ASC
	`, orderID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer rows.Close()

	var events []domain.OrderEvent
	for rows.Next() {
		var ev domain.OrderEvent
		var raw []byte
		if err := rows.Scan(&ev.ID, &ev.BranchID, &ev.OrderID, &ev.EventType, &ev.ActorID, &raw, &ev.CreatedAt); err == nil {
			ev.Payload = raw
			events = append(events, ev)
		}
	}

	rebuilt, err := domain.RebuildOrderStateFromEvents(events)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "REBUILD_ERROR", err.Error())
	}

	isConsistent := (rebuilt.SubtotalKurus == dbSub) &&
		(rebuilt.DiscountKurus == dbDisc) &&
		(rebuilt.TotalKurus == dbTot) &&
		(rebuilt.PaidKurus == dbPaid) &&
		(rebuilt.TipKurus == dbTip)

	return c.JSON(fiber.Map{
		"order_id":      orderID,
		"version":       dbVer,
		"is_consistent": isConsistent,
		"materialized": fiber.Map{
			"subtotal_kurus": dbSub,
			"discount_kurus": dbDisc,
			"total_kurus":    dbTot,
			"paid_kurus":     dbPaid,
			"tip_kurus":      dbTip,
			"status":         dbStatus,
		},
		"rebuilt_from_events": rebuilt,
		"events":              events,
	})
}

// recalculateOrderTotalsTx recomputes subtotal, re-allocates discount across active items with zero kurus drift,
// and increments the order's optimistic lock version.
func (s *POSService) recalculateOrderTotalsTx(ctx context.Context, tx pgx.Tx, orderID int64) error {
	var currentDiscount, currentPaid int64
	var currentStatus string
	err := tx.QueryRow(ctx, `
		SELECT discount_kurus, paid_kurus, status
		FROM "dz-orders"
		WHERE id = $1
	`, orderID).Scan(&currentDiscount, &currentPaid, &currentStatus)
	if err != nil {
		return err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, item_name, quantity, unit_price_kurus, line_subtotal_kurus, paid_kurus, is_cancelled
		FROM "dz-order-items"
		WHERE order_id = $1
		ORDER BY id ASC
	`, orderID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var inputs []domain.LineItemInput
	var subtotal int64
	for rows.Next() {
		var it domain.LineItemInput
		if err := rows.Scan(&it.ID, &it.Name, &it.Quantity, &it.UnitPriceKurus, &it.LineSubtotalKurus, &it.PaidKurus, &it.IsCancelled); err == nil {
			inputs = append(inputs, it)
			if !it.IsCancelled {
				subtotal += it.LineSubtotalKurus
			}
		}
	}
	rows.Close()

	if currentDiscount > subtotal {
		currentDiscount = subtotal
	}

	allocated, err := domain.AllocateDiscountAcrossItems(inputs, currentDiscount)
	if err != nil {
		return err
	}

	for _, alloc := range allocated {
		_, err := tx.Exec(ctx, `
			UPDATE "dz-order-items"
			SET allocated_discount_kurus = $1, net_total_kurus = $2, updated_at = NOW()
			WHERE id = $3
		`, alloc.AllocatedDiscountKurus, alloc.NetTotalKurus, alloc.ID)
		if err != nil {
			return err
		}
	}

	total := subtotal - currentDiscount
	newStatus := currentStatus
	if newStatus != "MERGED" && newStatus != "CANCELLED" {
		if total > 0 && currentPaid >= total {
			newStatus = "PAID"
		} else if currentPaid > 0 {
			newStatus = "PARTIALLY_PAID"
		} else {
			newStatus = "OPEN"
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE "dz-orders"
		SET subtotal_kurus = $1, discount_kurus = $2, total_kurus = $3, status = $4,
		    version = version + 1, updated_at = NOW()
		WHERE id = $5
	`, subtotal, currentDiscount, total, newStatus, orderID)
	return err
}

func (s *POSService) deductRecipeStockTx(
	ctx context.Context,
	tx pgx.Tx,
	branchID, orderID, menuItemID int64,
	quantity int,
	variantMultiplierPct int,
	modifiers []domain.ModifierRecipeEffect,
) error {
	edgeRows, err := tx.Query(ctx, `
		SELECT COALESCE(menu_item_id, 0), COALESCE(semi_finished_code, ''), ingredient_code, qty_grams
		FROM "dz-recipes"
	`)
	if err != nil {
		return err
	}
	defer edgeRows.Close()

	var edges []domain.RecipeEdge
	for edgeRows.Next() {
		var e domain.RecipeEdge
		if err := edgeRows.Scan(&e.MenuItemID, &e.SemiFinishedCode, &e.IngredientCode, &e.QtyGrams); err == nil {
			edges = append(edges, e)
		}
	}
	edgeRows.Close()

	reqs, err := domain.ResolveRecipeTree(menuItemID, quantity, variantMultiplierPct, modifiers, edges)
	if err != nil {
		return err
	}

	for _, r := range reqs {
		var ingID, costPerUnit int64
		err := tx.QueryRow(ctx, `
			UPDATE "dz-ingredients"
			SET theoretical_qty_grams = theoretical_qty_grams - $1, updated_at = NOW()
			WHERE branch_id = $2 AND code = $3
			RETURNING id, cost_per_unit_kurus
		`, r.TotalGrams, branchID, r.IngredientCode).Scan(&ingID, &costPerUnit)
		if err != nil {
			continue
		}

		reason := "Sipariş reçete düşümü"
		if r.FromSubRecipe {
			reason = fmt.Sprintf("Alt reçete (%s) üzerinden hammadde düşümü", r.SubRecipeSource)
		}
		_, _ = tx.Exec(ctx, `
			INSERT INTO "dz-stock-movements" (branch_id, ingredient_id, order_id, movement_type, delta_grams, cost_kurus, reason)
			VALUES ($1, $2, $3, 'SALE_DEDUCTION', $4, $5, $6)
		`, branchID, ingID, orderID, -r.TotalGrams, r.TotalGrams*costPerUnit, reason)
	}

	return nil
}

func (s *POSService) handleAddOrderItem(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	idempKey, replayed := s.checkIdempotency(ctx, c, claims.UserID)
	if replayed {
		return nil
	}

	orderID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || orderID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_ORDER_ID", "Geçersiz adisyon kimliği.")
	}

	var req struct {
		MenuItemID      int64   `json:"menu_item_id"`
		Quantity        int     `json:"quantity"`
		VariantID       int64   `json:"variant_id"`
		ModifierIDs     []int64 `json:"modifier_ids"`
		ExpectedVersion int64   `json:"expected_version"`
	}
	if err := c.BodyParser(&req); err != nil {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_JSON", "Geçersiz istek gövdesi.")
	}
	if req.MenuItemID <= 0 || req.Quantity <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "menu_item_id ve pozitif quantity zorunludur.")
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TX_ERROR", err.Error())
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var branchID, currentVersion int64
	var orderStatus string
	err = tx.QueryRow(ctx, `
		SELECT branch_id, version, status
		FROM "dz-orders"
		WHERE id = $1
		FOR UPDATE
	`, orderID).Scan(&branchID, &currentVersion, &orderStatus)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "ORDER_NOT_FOUND", "Adisyon bulunamadı.")
	}

	if orderStatus == "MERGED" || orderStatus == "CANCELLED" {
		return auth.RespondError(c, fiber.StatusConflict, "ORDER_CLOSED", "Birleştirilmiş veya iptal edilmiş adisyona kalem eklenemez.")
	}

	// Optimistic locking check when ExpectedVersion > 0
	if req.ExpectedVersion > 0 && req.ExpectedVersion != currentVersion {
		return auth.RespondError(c, fiber.StatusConflict, "OPTIMISTIC_LOCK_CONFLICT",
			fmt.Sprintf("Adisyon versiyon uyuşmazlığı: beklenen=%d, mevcut=%d", req.ExpectedVersion, currentVersion))
	}

	// Fetch menu item
	var itemName, station string
	var basePrice int64
	var course, prepSec int
	err = tx.QueryRow(ctx, `
		SELECT name, base_price_kurus, station, course, prep_time_sec
		FROM "dz-menu-items"
		WHERE id = $1 AND is_available = TRUE
	`, req.MenuItemID).Scan(&itemName, &basePrice, &station, &course, &prepSec)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "MENU_ITEM_NOT_FOUND", "Menü ürünü bulunamadı.")
	}

	variantName := "Standart"
	var variantDelta int64
	variantMultiplierPct := 100
	var variantIDPtr *int64
	if req.VariantID > 0 {
		err = tx.QueryRow(ctx, `
			SELECT name, price_delta_kurus, recipe_multiplier_pct
			FROM "dz-menu-variants"
			WHERE id = $1 AND menu_item_id = $2
		`, req.VariantID, req.MenuItemID).Scan(&variantName, &variantDelta, &variantMultiplierPct)
		if err == nil {
			variantIDPtr = &req.VariantID
		}
	}

	var modDelta int64
	var modNames []string
	var modEffects []domain.ModifierRecipeEffect
	for _, modID := range req.ModifierIDs {
		var mName, ingCode string
		var mPrice, extraGrams int64
		if err := tx.QueryRow(ctx, `
			SELECT name, price_delta_kurus, ingredient_code, extra_qty_grams
			FROM "dz-menu-modifiers"
			WHERE id = $1 AND menu_item_id = $2
		`, modID, req.MenuItemID).Scan(&mName, &mPrice, &ingCode, &extraGrams); err == nil {
			modDelta += mPrice
			modNames = append(modNames, mName)
			modEffects = append(modEffects, domain.ModifierRecipeEffect{
				Name:           mName,
				IngredientCode: ingCode,
				ExtraQtyGrams:  extraGrams,
			})
		}
	}

	unitPrice := basePrice + variantDelta + modDelta
	if unitPrice < 0 {
		unitPrice = 0
	}
	lineSubtotal := unitPrice * int64(req.Quantity)
	modIDsJSON, _ := json.Marshal(req.ModifierIDs)
	modNamesStr := strings.Join(modNames, ", ")

	var orderItemID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO "dz-order-items" (
			order_id, menu_item_id, item_name, variant_id, variant_name, modifier_ids, modifier_names,
			quantity, unit_price_kurus, line_subtotal_kurus, net_total_kurus, station, course, prep_time_sec
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, $11, $12, $13)
		RETURNING id
	`, orderID, req.MenuItemID, itemName, variantIDPtr, variantName, modIDsJSON, modNamesStr,
		req.Quantity, unitPrice, lineSubtotal, station, course, prepSec).Scan(&orderItemID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "INSERT_ITEM_ERROR", err.Error())
	}

	// Deduct theoretical stock via recipe + sub-recipe resolver
	_ = s.deductRecipeStockTx(ctx, tx, branchID, orderID, req.MenuItemID, req.Quantity, variantMultiplierPct, modEffects)

	// Record event in "dz-order-events"
	evID, err := s.appendOrderEventTx(ctx, tx, branchID, orderID, claims.UserID, domain.EventItemAdded, domain.ItemAddedPayload{
		OrderItemID:       orderItemID,
		MenuItemID:        req.MenuItemID,
		ItemName:          itemName,
		VariantName:       variantName,
		ModifierNames:     modNamesStr,
		Quantity:          req.Quantity,
		UnitPriceKurus:    unitPrice,
		LineSubtotalKurus: lineSubtotal,
		Station:           station,
		Course:            course,
	})
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "EVENT_LOG_ERROR", err.Error())
	}

	if err := s.recalculateOrderTotalsTx(ctx, tx, orderID); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "RECALC_ERROR", err.Error())
	}

	_ = s.writeAuditTx(ctx, tx, branchID, claims.UserID, claims.Role, "ORDER_ITEM_ADDED", "order_item", fmt.Sprintf("%d", orderItemID), nil, fiber.Map{
		"order_id":            orderID,
		"item_name":           itemName,
		"quantity":            req.Quantity,
		"line_subtotal_kurus": lineSubtotal,
	})

	if err := tx.Commit(ctx); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "COMMIT_ERROR", err.Error())
	}

	// Broadcast to KDS SSE broker
	s.broker.Publish(domain.KDSStreamEvent{
		EventID:   evID,
		BranchID:  branchID,
		OrderID:   orderID,
		Station:   station,
		EventType: domain.EventItemAdded,
		Summary:   fmt.Sprintf("%s (%s) x%d eklendi", itemName, variantName, req.Quantity),
		CreatedAt: time.Now().UTC(),
	})

	detail, _ := s.loadOrderDetail(ctx, orderID)
	return s.saveIdempotencyAndRespond(ctx, c, idempKey, claims.UserID, fiber.StatusCreated, detail)
}

func (s *POSService) handleCancelOrderItem(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	orderID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	itemID, err2 := strconv.ParseInt(c.Params("itemId"), 10, 64)
	if err != nil || err2 != nil || orderID <= 0 || itemID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_IDS", "Geçersiz adisyon veya kalem kimliği.")
	}

	var req struct {
		Reason       string `json:"reason"`
		ProduceWaste bool   `json:"produce_waste"`
	}
	_ = c.BodyParser(&req)
	if req.Reason == "" {
		req.Reason = "Garson/Müdür iptali"
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TX_ERROR", err.Error())
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var branchID, menuItemID, lineSub int64
	var itemName, station, kdsStatus string
	var qty int
	var isProduced, isCancelled bool

	err = tx.QueryRow(ctx, `
		SELECT o.branch_id, i.menu_item_id, i.item_name, i.quantity, i.line_subtotal_kurus,
		       i.station, i.kds_status, i.is_produced, i.is_cancelled
		FROM "dz-order-items" i
		JOIN "dz-orders" o ON o.id = i.order_id
		WHERE i.id = $1 AND i.order_id = $2
		FOR UPDATE
	`, itemID, orderID).Scan(&branchID, &menuItemID, &itemName, &qty, &lineSub, &station, &kdsStatus, &isProduced, &isCancelled)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "ITEM_NOT_FOUND", "Adisyon kalemi bulunamadı.")
	}

	if isCancelled {
		return auth.RespondError(c, fiber.StatusConflict, "ALREADY_CANCELLED", "Kalem zaten iptal edilmiş.")
	}

	wasteRecorded := isProduced || req.ProduceWaste || kdsStatus == "PREPARING" || kdsStatus == "READY" || kdsStatus == "SERVED"

	_, err = tx.Exec(ctx, `
		UPDATE "dz-order-items"
		SET is_cancelled = TRUE, is_produced = $1, kds_status = 'CANCELLED',
		    cancel_reason = $2, allocated_discount_kurus = 0, net_total_kurus = 0,
		    version = version + 1, updated_at = NOW()
		WHERE id = $3
	`, wasteRecorded, req.Reason, itemID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "UPDATE_ERROR", err.Error())
	}

	if wasteRecorded {
		// Record waste movement in "dz-stock-movements" and add waste cost to open shift
		var firstIngID, costPerUnit int64
		_ = tx.QueryRow(ctx, `
			SELECT id, cost_per_unit_kurus FROM "dz-ingredients"
			WHERE branch_id = $1 ORDER BY id ASC LIMIT 1
		`, branchID).Scan(&firstIngID, &costPerUnit)
		wasteGrams := int64(150 * qty)
		wasteCost := lineSub / 3
		if firstIngID > 0 {
			_, _ = tx.Exec(ctx, `
				INSERT INTO "dz-stock-movements" (branch_id, ingredient_id, order_id, movement_type, delta_grams, cost_kurus, reason)
				VALUES ($1, $2, $3, 'WASTE', $4, $5, $6)
			`, branchID, firstIngID, orderID, -wasteGrams, wasteCost, "Üretilmiş kalem iptali (Fire): "+itemName+" - "+req.Reason)
		}
		_, _ = tx.Exec(ctx, `
			UPDATE "dz-shifts"
			SET total_waste_kurus = total_waste_kurus + $1
			WHERE branch_id = $2 AND status = 'OPEN'
		`, wasteCost, branchID)
	}

	evID, err := s.appendOrderEventTx(ctx, tx, branchID, orderID, claims.UserID, domain.EventItemCancelled, domain.ItemCancelledPayload{
		OrderItemID:       itemID,
		LineSubtotalKurus: lineSub,
		IsProduced:        wasteRecorded,
		WasteRecorded:     wasteRecorded,
		Reason:            req.Reason,
	})
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "EVENT_LOG_ERROR", err.Error())
	}

	if err := s.recalculateOrderTotalsTx(ctx, tx, orderID); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "RECALC_ERROR", err.Error())
	}

	_ = s.writeAuditTx(ctx, tx, branchID, claims.UserID, claims.Role, "ORDER_ITEM_CANCELLED", "order_item", fmt.Sprintf("%d", itemID),
		fiber.Map{"item_name": itemName, "line_subtotal_kurus": lineSub},
		fiber.Map{"is_cancelled": true, "waste_recorded": wasteRecorded, "reason": req.Reason},
	)

	if err := tx.Commit(ctx); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "COMMIT_ERROR", err.Error())
	}

	s.broker.Publish(domain.KDSStreamEvent{
		EventID:   evID,
		BranchID:  branchID,
		OrderID:   orderID,
		Station:   station,
		EventType: domain.EventItemCancelled,
		Summary:   fmt.Sprintf("İPTAL/FİRE: %s (%s)", itemName, req.Reason),
		CreatedAt: time.Now().UTC(),
	})

	detail, _ := s.loadOrderDetail(ctx, orderID)
	return c.JSON(detail)
}

func (s *POSService) handleMoveOrderTable(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	orderID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || orderID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_ORDER_ID", "Geçersiz adisyon kimliği.")
	}

	var req struct {
		TargetTableID int64 `json:"target_table_id"`
	}
	if err := c.BodyParser(&req); err != nil || req.TargetTableID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "target_table_id zorunludur.")
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TX_ERROR", err.Error())
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var branchID, fromTableID int64
	err = tx.QueryRow(ctx, `
		SELECT branch_id, table_id FROM "dz-orders" WHERE id = $1 FOR UPDATE
	`, orderID).Scan(&branchID, &fromTableID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "ORDER_NOT_FOUND", "Adisyon bulunamadı.")
	}

	if fromTableID == req.TargetTableID {
		return auth.RespondError(c, fiber.StatusBadRequest, "SAME_TABLE", "Hedef masa mevcut masa ile aynı.")
	}

	var targetStatus string
	err = tx.QueryRow(ctx, `
		SELECT status FROM "dz-tables" WHERE id = $1 AND branch_id = $2 FOR UPDATE
	`, req.TargetTableID, branchID).Scan(&targetStatus)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "TARGET_TABLE_NOT_FOUND", "Hedef masa bulunamadı.")
	}
	if targetStatus != "EMPTY" {
		return auth.RespondError(c, fiber.StatusConflict, "TARGET_TABLE_BUSY", "Hedef masa boş değil; birleştirme (merge) kullanın.")
	}

	_, _ = tx.Exec(ctx, `
		UPDATE "dz-tables" SET status = 'EMPTY', active_order_id = NULL, version = version + 1, updated_at = NOW()
		WHERE id = $1
	`, fromTableID)

	_, _ = tx.Exec(ctx, `
		UPDATE "dz-tables" SET status = 'OCCUPIED', active_order_id = $1, version = version + 1, updated_at = NOW()
		WHERE id = $2
	`, orderID, req.TargetTableID)

	_, _ = tx.Exec(ctx, `
		UPDATE "dz-orders" SET table_id = $1, version = version + 1, updated_at = NOW()
		WHERE id = $2
	`, req.TargetTableID, orderID)

	_, _ = s.appendOrderEventTx(ctx, tx, branchID, orderID, claims.UserID, domain.EventTableMoved, domain.TableMovedPayload{
		FromTableID: fromTableID,
		ToTableID:   req.TargetTableID,
	})

	_ = s.writeAuditTx(ctx, tx, branchID, claims.UserID, claims.Role, "ORDER_TABLE_MOVED", "order", fmt.Sprintf("%d", orderID),
		fiber.Map{"table_id": fromTableID},
		fiber.Map{"table_id": req.TargetTableID},
	)

	if err := tx.Commit(ctx); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "COMMIT_ERROR", err.Error())
	}

	detail, _ := s.loadOrderDetail(ctx, orderID)
	return c.JSON(detail)
}

func (s *POSService) handleMergeOrders(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	var req struct {
		SourceOrderID int64 `json:"source_order_id"`
		TargetOrderID int64 `json:"target_order_id"`
	}
	if err := c.BodyParser(&req); err != nil || req.SourceOrderID <= 0 || req.TargetOrderID <= 0 || req.SourceOrderID == req.TargetOrderID {
		return auth.RespondError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "Farklı ve geçerli source_order_id ile target_order_id zorunludur.")
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TX_ERROR", err.Error())
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var srcBranch, srcTable, srcSub int64
	var srcStatus string
	err = tx.QueryRow(ctx, `
		SELECT branch_id, table_id, subtotal_kurus, status FROM "dz-orders" WHERE id = $1 FOR UPDATE
	`, req.SourceOrderID).Scan(&srcBranch, &srcTable, &srcSub, &srcStatus)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "SOURCE_NOT_FOUND", "Kaynak adisyon bulunamadı.")
	}

	var tgtBranch, tgtTable int64
	var tgtStatus string
	err = tx.QueryRow(ctx, `
		SELECT branch_id, table_id, status FROM "dz-orders" WHERE id = $1 FOR UPDATE
	`, req.TargetOrderID).Scan(&tgtBranch, &tgtTable, &tgtStatus)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "TARGET_NOT_FOUND", "Hedef adisyon bulunamadı.")
	}

	// Move active items from source order to target order and record ITEM_ADDED events on target
	itemRows, err := tx.Query(ctx, `
		SELECT id, menu_item_id, item_name, variant_name, modifier_names, quantity, unit_price_kurus, line_subtotal_kurus, station, course
		FROM "dz-order-items"
		WHERE order_id = $1 AND is_cancelled = FALSE
		ORDER BY id ASC
	`, req.SourceOrderID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}

	var movedItems []domain.ItemAddedPayload
	for itemRows.Next() {
		var p domain.ItemAddedPayload
		if err := itemRows.Scan(&p.OrderItemID, &p.MenuItemID, &p.ItemName, &p.VariantName, &p.ModifierNames, &p.Quantity, &p.UnitPriceKurus, &p.LineSubtotalKurus, &p.Station, &p.Course); err == nil {
			movedItems = append(movedItems, p)
		}
	}
	itemRows.Close()

	_, _ = tx.Exec(ctx, `
		UPDATE "dz-order-items" SET order_id = $1, updated_at = NOW()
		WHERE order_id = $2 AND is_cancelled = FALSE
	`, req.TargetOrderID, req.SourceOrderID)

	_, _ = s.appendOrderEventTx(ctx, tx, tgtBranch, req.TargetOrderID, claims.UserID, domain.EventTableMerged, domain.TableMergedPayload{
		SourceOrderID:         req.SourceOrderID,
		TargetOrderID:         req.TargetOrderID,
		TransferredTotalKurus: srcSub,
	})

	for _, p := range movedItems {
		_, _ = s.appendOrderEventTx(ctx, tx, tgtBranch, req.TargetOrderID, claims.UserID, domain.EventItemAdded, p)
	}

	_, _ = tx.Exec(ctx, `
		UPDATE "dz-orders"
		SET status = 'MERGED', subtotal_kurus = 0, discount_kurus = 0, total_kurus = 0, version = version + 1, closed_at = NOW()
		WHERE id = $1
	`, req.SourceOrderID)

	_, _ = tx.Exec(ctx, `
		UPDATE "dz-tables"
		SET status = 'EMPTY', active_order_id = NULL, merged_into_table_id = $1, version = version + 1, updated_at = NOW()
		WHERE id = $2
	`, tgtTable, srcTable)

	if err := s.recalculateOrderTotalsTx(ctx, tx, req.TargetOrderID); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "RECALC_ERROR", err.Error())
	}

	_ = s.writeAuditTx(ctx, tx, tgtBranch, claims.UserID, claims.Role, "ORDERS_MERGED", "order", fmt.Sprintf("%d", req.TargetOrderID),
		fiber.Map{"source_order_id": req.SourceOrderID},
		fiber.Map{"target_order_id": req.TargetOrderID, "transferred_items": len(movedItems)},
	)

	if err := tx.Commit(ctx); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "COMMIT_ERROR", err.Error())
	}

	detail, _ := s.loadOrderDetail(ctx, req.TargetOrderID)
	return c.JSON(detail)
}

func (s *POSService) handleListCampaigns(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	ctx := c.UserContext()

	cRows, err := s.queries(ctx).Query(ctx, `
		SELECT id, code, name, rule_type, priority, stackable, config_json, is_active
		FROM "dz-campaigns"
		ORDER BY priority DESC, id ASC
	`)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer cRows.Close()

	var campaigns []fiber.Map
	for cRows.Next() {
		var id int64
		var code, name, rType string
		var prio int
		var stack, active bool
		var cfgRaw []byte
		if err := cRows.Scan(&id, &code, &name, &rType, &prio, &stack, &cfgRaw, &active); err == nil {
			var cfgObj any
			_ = json.Unmarshal(cfgRaw, &cfgObj)
			campaigns = append(campaigns, fiber.Map{
				"id":        id,
				"code":      code,
				"name":      name,
				"rule_type": rType,
				"priority":  prio,
				"stackable": stack,
				"config":    cfgObj,
				"is_active": active,
			})
		}
	}

	cpRows, err := s.queries(ctx).Query(ctx, `
		SELECT id, code, description, discount_type, discount_value, min_order_kurus, max_discount_kurus, stackable, is_active
		FROM "dz-coupons"
		ORDER BY id ASC
	`)
	var coupons []fiber.Map
	if err == nil {
		defer cpRows.Close()
		for cpRows.Next() {
			var id, val, minOrd, maxDisc int64
			var code, desc, dType string
			var stack, active bool
			if err := cpRows.Scan(&id, &code, &desc, &dType, &val, &minOrd, &maxDisc, &stack, &active); err == nil {
				coupons = append(coupons, fiber.Map{
					"id":                 id,
					"code":               code,
					"description":        desc,
					"discount_type":      dType,
					"discount_value":     val,
					"min_order_kurus":    minOrd,
					"max_discount_kurus": maxDisc,
					"stackable":          stack,
					"is_active":          active,
				})
			}
		}
	}

	return c.JSON(fiber.Map{
		"campaigns": campaigns,
		"coupons":   coupons,
	})
}

func (s *POSService) handleApplyCampaigns(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	orderID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || orderID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_ORDER_ID", "Geçersiz adisyon kimliği.")
	}

	var req struct {
		CouponCode  string `json:"coupon_code"`
		CurrentHour *int   `json:"current_hour"`
	}
	_ = c.BodyParser(&req)
	req.CouponCode = strings.TrimSpace(strings.ToUpper(req.CouponCode))

	hour := time.Now().Hour()
	if req.CurrentHour != nil {
		hour = *req.CurrentHour
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TX_ERROR", err.Error())
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var branchID int64
	if err := tx.QueryRow(ctx, `SELECT branch_id FROM "dz-orders" WHERE id = $1 FOR UPDATE`, orderID).Scan(&branchID); err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "ORDER_NOT_FOUND", "Adisyon bulunamadı.")
	}

	// Load basket items
	iRows, err := tx.Query(ctx, `
		SELECT m.code, i.item_name, i.quantity, i.unit_price_kurus, i.line_subtotal_kurus
		FROM "dz-order-items" i
		JOIN "dz-menu-items" m ON m.id = i.menu_item_id
		WHERE i.order_id = $1 AND i.is_cancelled = FALSE
	`, orderID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	var basket []domain.BasketItem
	for iRows.Next() {
		var b domain.BasketItem
		if err := iRows.Scan(&b.MenuItemCode, &b.ItemName, &b.Quantity, &b.UnitPriceKurus, &b.LineSubtotalKurus); err == nil {
			basket = append(basket, b)
		}
	}
	iRows.Close()

	// Load campaigns
	cRows, err := tx.Query(ctx, `
		SELECT code, name, rule_type, priority, stackable, config_json, is_active
		FROM "dz-campaigns"
		WHERE is_active = TRUE
	`)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	var campaigns []domain.CampaignRule
	for cRows.Next() {
		var r domain.CampaignRule
		var raw []byte
		if err := cRows.Scan(&r.Code, &r.Name, &r.RuleType, &r.Priority, &r.Stackable, &raw, &r.IsActive); err == nil {
			r.ConfigJSON = raw
			campaigns = append(campaigns, r)
		}
	}
	cRows.Close()

	// Load optional coupon
	var couponPtr *domain.CouponRule
	if req.CouponCode != "" {
		var cp domain.CouponRule
		err := tx.QueryRow(ctx, `
			SELECT code, description, discount_type, discount_value, min_order_kurus, max_discount_kurus, stackable, is_active
			FROM "dz-coupons"
			WHERE UPPER(code) = $1
		`, req.CouponCode).Scan(&cp.Code, &cp.Description, &cp.DiscountType, &cp.DiscountValue, &cp.MinOrderKurus, &cp.MaxDiscountKurus, &cp.Stackable, &cp.IsActive)
		if err == nil {
			couponPtr = &cp
		}
	}

	evalResult := domain.EvaluateBestCampaigns(basket, campaigns, couponPtr, hour)
	expJSON, _ := json.Marshal(evalResult.AllEvaluations)

	_, err = tx.Exec(ctx, `
		UPDATE "dz-orders"
		SET discount_kurus = $1, coupon_code = $2, campaign_explanation = $3
		WHERE id = $4
	`, evalResult.TotalDiscountKurus, req.CouponCode, expJSON, orderID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "UPDATE_ERROR", err.Error())
	}

	_, err = s.appendOrderEventTx(ctx, tx, branchID, orderID, claims.UserID, domain.EventCampaignApplied, domain.CampaignAppliedPayload{
		DiscountKurus: evalResult.TotalDiscountKurus,
		CouponCode:    req.CouponCode,
	})
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "EVENT_LOG_ERROR", err.Error())
	}

	if err := s.recalculateOrderTotalsTx(ctx, tx, orderID); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "RECALC_ERROR", err.Error())
	}

	_ = s.writeAuditTx(ctx, tx, branchID, claims.UserID, claims.Role, "CAMPAIGN_EVALUATED", "order", fmt.Sprintf("%d", orderID), nil, evalResult)

	if err := tx.Commit(ctx); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "COMMIT_ERROR", err.Error())
	}

	detail, _ := s.loadOrderDetail(ctx, orderID)
	return c.JSON(fiber.Map{
		"evaluation": evalResult,
		"order":      detail,
	})
}

func (s *POSService) handleSplitPreview(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	ctx := c.UserContext()
	orderID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || orderID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_ORDER_ID", "Geçersiz adisyon kimliği.")
	}

	var req struct {
		SplitMode       string  `json:"split_mode"` // "EQUAL", "BY_ITEM", "CUSTOM"
		Parts           int     `json:"parts"`
		SelectedItemIDs []int64 `json:"selected_item_ids"`
		CustomKurus     int64   `json:"custom_kurus"`
	}
	if err := c.BodyParser(&req); err != nil {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON gövdesi.")
	}

	var total, paid int64
	if err := s.queries(ctx).QueryRow(ctx, `SELECT total_kurus, paid_kurus FROM "dz-orders" WHERE id = $1`, orderID).Scan(&total, &paid); err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "ORDER_NOT_FOUND", "Adisyon bulunamadı.")
	}
	remaining := max64(0, total-paid)

	switch strings.ToUpper(req.SplitMode) {
	case "EQUAL":
		shares, err := domain.CalculateEqualSplit(remaining, req.Parts)
		if err != nil {
			return auth.RespondError(c, fiber.StatusBadRequest, "SPLIT_ERROR", err.Error())
		}
		return c.JSON(fiber.Map{
			"split_mode":      "EQUAL",
			"remaining_kurus": remaining,
			"parts":           req.Parts,
			"shares_kurus":    shares,
		})

	case "BY_ITEM":
		rows, err := s.queries(ctx).Query(ctx, `
			SELECT id, item_name, quantity, unit_price_kurus, line_subtotal_kurus,
			       allocated_discount_kurus, net_total_kurus, paid_kurus
			FROM "dz-order-items"
			WHERE order_id = $1 AND is_cancelled = FALSE
		`, orderID)
		if err != nil {
			return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
		}
		defer rows.Close()
		var items []domain.AllocatedLineItem
		for rows.Next() {
			var it domain.AllocatedLineItem
			if err := rows.Scan(&it.ID, &it.Name, &it.Quantity, &it.UnitPriceKurus, &it.LineSubtotalKurus, &it.AllocatedDiscountKurus, &it.NetTotalKurus, &it.PaidKurus); err == nil {
				it.RemainingKurus = max64(0, it.NetTotalKurus-it.PaidKurus)
				items = append(items, it)
			}
		}
		due, err := domain.CalculateItemSplit(items, req.SelectedItemIDs)
		if err != nil {
			return auth.RespondError(c, fiber.StatusBadRequest, "SPLIT_ERROR", err.Error())
		}
		return c.JSON(fiber.Map{
			"split_mode":         "BY_ITEM",
			"selected_item_ids":  req.SelectedItemIDs,
			"selected_due_kurus": due,
			"order_remaining":    remaining,
		})

	default:
		return c.JSON(fiber.Map{
			"split_mode":      "CUSTOM",
			"remaining_kurus": remaining,
			"custom_kurus":    req.CustomKurus,
		})
	}
}

func (s *POSService) handleCreatePayment(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	claims, _ := auth.GetClaims(c)
	ctx := c.UserContext()

	idempKey, replayed := s.checkIdempotency(ctx, c, claims.UserID)
	if replayed {
		return nil
	}

	orderID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || orderID <= 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_ORDER_ID", "Geçersiz adisyon kimliği.")
	}

	var req struct {
		Method          string  `json:"method"`     // "CASH", "CARD", or "MIXED"
		SplitMode       string  `json:"split_mode"` // "FULL", "EQUAL", "BY_ITEM", "CUSTOM", "MIXED"
		AmountKurus     int64   `json:"amount_kurus"`
		CashAmountKurus int64   `json:"cash_amount_kurus"`
		CardAmountKurus int64   `json:"card_amount_kurus"`
		TipKurus        int64   `json:"tip_kurus"`
		SelectedItemIDs []int64 `json:"selected_item_ids"`
	}
	if err := c.BodyParser(&req); err != nil {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON gövdesi.")
	}

	req.Method = strings.ToUpper(strings.TrimSpace(req.Method))
	req.SplitMode = strings.ToUpper(strings.TrimSpace(req.SplitMode))
	if req.SplitMode == "" {
		req.SplitMode = "FULL"
	}
	if req.TipKurus < 0 {
		return auth.RespondError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "Bahşiş negatif olamaz.")
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TX_ERROR", err.Error())
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var branchID, tableID, sub, disc, total, paid int64
	var status string
	err = tx.QueryRow(ctx, `
		SELECT branch_id, table_id, subtotal_kurus, discount_kurus, total_kurus, paid_kurus, status
		FROM "dz-orders"
		WHERE id = $1
		FOR UPDATE
	`, orderID).Scan(&branchID, &tableID, &sub, &disc, &total, &paid, &status)
	if err != nil {
		return auth.RespondError(c, fiber.StatusNotFound, "ORDER_NOT_FOUND", "Adisyon bulunamadı.")
	}

	remaining := max64(0, total-paid)
	if remaining <= 0 {
		return auth.RespondError(c, fiber.StatusConflict, "ALREADY_PAID", "Adisyonun kalan borcu bulunmamaktadır.")
	}

	// Determine payment legs (supports single CASH/CARD or MIXED cash+card in a single atomic transaction)
	type paymentLeg struct {
		method string
		amount int64
		tip    int64
	}
	var legs []paymentLeg

	if req.Method == "MIXED" || (req.CashAmountKurus > 0 && req.CardAmountKurus > 0) {
		if req.CashAmountKurus <= 0 || req.CardAmountKurus <= 0 {
			return auth.RespondError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "Karışık (MIXED) ödemede hem nakit hem kart tutarı pozitif olmalıdır.")
		}
		legs = append(legs,
			paymentLeg{method: "CASH", amount: req.CashAmountKurus, tip: 0},
			paymentLeg{method: "CARD", amount: req.CardAmountKurus, tip: req.TipKurus},
		)
	} else {
		if req.Method != "CASH" && req.Method != "CARD" {
			req.Method = "CARD"
		}
		amt := req.AmountKurus
		if req.SplitMode == "BY_ITEM" && len(req.SelectedItemIDs) > 0 && amt == 0 {
			rows, err := tx.Query(ctx, `
				SELECT id, net_total_kurus, paid_kurus
				FROM "dz-order-items"
				WHERE order_id = $1 AND is_cancelled = FALSE
				FOR UPDATE
			`, orderID)
			if err != nil {
				return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
			}
			var items []domain.AllocatedLineItem
			for rows.Next() {
				var it domain.AllocatedLineItem
				if err := rows.Scan(&it.ID, &it.NetTotalKurus, &it.PaidKurus); err == nil {
					it.RemainingKurus = max64(0, it.NetTotalKurus-it.PaidKurus)
					items = append(items, it)
				}
			}
			rows.Close()
			calcAmt, err := domain.CalculateItemSplit(items, req.SelectedItemIDs)
			if err != nil {
				return auth.RespondError(c, fiber.StatusBadRequest, "SPLIT_ERROR", err.Error())
			}
			amt = calcAmt
		} else if amt <= 0 && req.SplitMode == "FULL" {
			amt = remaining
		}
		if amt <= 0 {
			return auth.RespondError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "Ödeme tutarı (amount_kurus) pozitif olmalıdır.")
		}
		legs = append(legs, paymentLeg{method: req.Method, amount: amt, tip: req.TipKurus})
	}

	var totalPaymentThisCall int64
	for _, leg := range legs {
		totalPaymentThisCall += leg.amount
	}
	if totalPaymentThisCall > remaining {
		return auth.RespondError(c, fiber.StatusBadRequest, "OVERPAYMENT",
			fmt.Sprintf("Ödeme tutarı (%d kr) kalan adisyon tutarını (%d kr) aşamaz.", totalPaymentThisCall, remaining))
	}

	// Mark selected items paid if BY_ITEM
	if len(req.SelectedItemIDs) > 0 {
		for _, itemID := range req.SelectedItemIDs {
			_, _ = tx.Exec(ctx, `
				UPDATE "dz-order-items"
				SET paid_kurus = net_total_kurus, updated_at = NOW()
				WHERE id = $1 AND order_id = $2
			`, itemID, orderID)
		}
	}

	var shiftID *int64
	var openShiftID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM "dz-shifts" WHERE branch_id = $1 AND status = 'OPEN' ORDER BY id DESC LIMIT 1`, branchID).Scan(&openShiftID); err == nil {
		shiftID = &openShiftID
	}

	paidItemsJSON, _ := json.Marshal(req.SelectedItemIDs)

	for i, leg := range legs {
		legKey := idempKey
		if legKey == "" {
			legKey = fmt.Sprintf("pay-%d-%d-%d", orderID, time.Now().UnixNano(), i)
		} else if len(legs) > 1 {
			legKey = fmt.Sprintf("%s-leg-%d", legKey, i+1)
		}

		var payID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO "dz-payments" (
				branch_id, order_id, shift_id, method, split_mode, amount_kurus, tip_kurus, idempotency_key, paid_item_ids, cashier_id
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING id
		`, branchID, orderID, shiftID, leg.method, req.SplitMode, leg.amount, leg.tip, legKey, paidItemsJSON, claims.UserID).Scan(&payID)
		if err != nil {
			return auth.RespondError(c, fiber.StatusInternalServerError, "PAYMENT_INSERT_ERROR", err.Error())
		}

		_, err = s.appendOrderEventTx(ctx, tx, branchID, orderID, claims.UserID, domain.EventPaymentReceived, domain.PaymentReceivedPayload{
			PaymentID:   payID,
			Method:      leg.method,
			SplitMode:   req.SplitMode,
			AmountKurus: leg.amount,
			TipKurus:    leg.tip,
		})
		if err != nil {
			return auth.RespondError(c, fiber.StatusInternalServerError, "EVENT_LOG_ERROR", err.Error())
		}

		if shiftID != nil {
			if leg.method == "CASH" {
				_, _ = tx.Exec(ctx, `
					UPDATE "dz-shifts"
					SET cash_payments_kurus = cash_payments_kurus + $1,
					    total_sales_kurus = total_sales_kurus + $1,
					    total_tips_kurus = total_tips_kurus + $2
					WHERE id = $3
				`, leg.amount, leg.tip, *shiftID)
			} else {
				_, _ = tx.Exec(ctx, `
					UPDATE "dz-shifts"
					SET card_payments_kurus = card_payments_kurus + $1,
					    total_sales_kurus = total_sales_kurus + $1,
					    total_tips_kurus = total_tips_kurus + $2
					WHERE id = $3
				`, leg.amount, leg.tip, *shiftID)
			}
		}
	}

	newPaid := paid + totalPaymentThisCall
	newStatus := "PARTIALLY_PAID"
	if newPaid >= total {
		newStatus = "PAID"
	}

	_, err = tx.Exec(ctx, `
		UPDATE "dz-orders"
		SET paid_kurus = paid_kurus + $1, tip_kurus = tip_kurus + $2, status = $3,
		    version = version + 1, updated_at = NOW(),
		    closed_at = CASE WHEN $3 = 'PAID' THEN NOW() ELSE closed_at END
		WHERE id = $4
	`, totalPaymentThisCall, req.TipKurus, newStatus, orderID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "ORDER_UPDATE_ERROR", err.Error())
	}

	if newStatus == "PAID" {
		_, _ = tx.Exec(ctx, `
			UPDATE "dz-tables"
			SET status = 'EMPTY', active_order_id = NULL, version = version + 1, updated_at = NOW()
			WHERE id = $1
		`, tableID)
	}

	_ = s.writeAuditTx(ctx, tx, branchID, claims.UserID, claims.Role, "PAYMENT_PROCESSED", "order", fmt.Sprintf("%d", orderID),
		fiber.Map{"previous_paid_kurus": paid},
		fiber.Map{"new_paid_kurus": newPaid, "tip_kurus": req.TipKurus, "status": newStatus},
	)

	if err := tx.Commit(ctx); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "COMMIT_ERROR", err.Error())
	}

	detail, _ := s.loadOrderDetail(ctx, orderID)
	return s.saveIdempotencyAndRespond(ctx, c, idempKey, claims.UserID, fiber.StatusCreated, detail)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

var _ = errors.New
