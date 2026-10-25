package server

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/efehidir/ddo/backend/internal/auth"
	"github.com/efehidir/ddo/backend/internal/database"
)

func (s *POSService) handleListDemoAccounts(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"default_password": database.DefaultDemoPassword,
		"accounts": []fiber.Map{
			{"role": "waiter", "label": "Garson", "email": "garson@sofrapos.local", "password": database.DefaultDemoPassword},
			{"role": "cashier", "label": "Kasiyer", "email": "kasiyer@sofrapos.local", "password": database.DefaultDemoPassword},
			{"role": "chef", "label": "Aşçı (Mutfak - KDS)", "email": "asci@sofrapos.local", "password": database.DefaultDemoPassword},
			{"role": "branch_manager", "label": "Şube Müdürü", "email": "mudur@sofrapos.local", "password": database.DefaultDemoPassword},
			{"role": "hq_manager", "label": "Merkez Yönetim", "email": "merkez@sofrapos.local", "password": database.DefaultDemoPassword},
			{"role": "customer", "label": "Müşteri (QR Menü)", "email": "musteri@sofrapos.local", "password": database.DefaultDemoPassword},
			{"role": "hq_manager", "label": "Seed Kullanıcı", "email": "seed.ceylan@ddo-case.invalid", "password": database.DefaultDemoPassword},
		},
	})
}

func (s *POSService) handleLogin(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_JSON", "Geçersiz istek gövdesi.")
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		return auth.RespondError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "E-posta ve parola zorunludur.")
	}

	var (
		userID   int64
		branchID int64
		email    string
		passHash string
		fullName string
		role     string
		isActive bool
	)

	err := s.queries(c.UserContext()).QueryRow(c.UserContext(), `
		SELECT id, branch_id, email, password_hash, full_name, role, is_active
		FROM "dz-sk-users"
		WHERE LOWER(email) = $1
	`, req.Email).Scan(&userID, &branchID, &email, &passHash, &fullName, &role, &isActive)
	if err != nil || !isActive || !auth.VerifyPassword(passHash, req.Password) {
		return auth.RespondError(c, fiber.StatusUnauthorized, "INVALID_CREDENTIALS", "E-posta veya parola hatalı.")
	}

	tokens, err := auth.GenerateTokenPair(s.cfg.JWTSecret, userID, branchID, email, fullName, role)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TOKEN_ERROR", "Oturum anahtarı üretilemedi.")
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"token_type":    tokens.TokenType,
		"expires_in":    tokens.ExpiresIn,
		"user": fiber.Map{
			"id":        userID,
			"branch_id": branchID,
			"email":     email,
			"full_name": fullName,
			"role":      role,
		},
	})
}

func (s *POSService) handleRefresh(c *fiber.Ctx) error {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.BodyParser(&req); err != nil || req.RefreshToken == "" {
		return auth.RespondError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "refresh_token zorunludur.")
	}

	claims, err := auth.VerifyJWT(s.cfg.JWTSecret, req.RefreshToken)
	if err != nil || claims.TokenType != "refresh" {
		return auth.RespondError(c, fiber.StatusUnauthorized, "INVALID_REFRESH_TOKEN", "Geçersiz veya süresi dolmuş yenileme anahtarı.")
	}
	if !s.requirePool(c) {
		return nil
	}
	if err := s.pool.QueryRow(c.UserContext(), `SELECT branch_id, email, full_name, role FROM "dz-sk-users" WHERE id=$1 AND is_active=TRUE`, claims.UserID).Scan(&claims.BranchID, &claims.Email, &claims.FullName, &claims.Role); err != nil {
		return auth.RespondError(c, fiber.StatusUnauthorized, "INVALID_REFRESH_TOKEN", "Kullanıcı hesabı aktif değil.")
	}

	tokens, err := auth.GenerateTokenPair(s.cfg.JWTSecret, claims.UserID, claims.BranchID, claims.Email, claims.FullName, claims.Role)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TOKEN_ERROR", "Anahtar yenilenemedi.")
	}

	return c.JSON(tokens)
}

func (s *POSService) handleMe(c *fiber.Ctx) error {
	claims, _ := auth.GetClaims(c)
	return c.JSON(fiber.Map{
		"user": fiber.Map{
			"id":        claims.UserID,
			"branch_id": claims.BranchID,
			"email":     claims.Email,
			"full_name": claims.FullName,
			"role":      claims.Role,
		},
	})
}

func (s *POSService) handleListBranches(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}

	rows, err := s.queries(c.UserContext()).Query(c.UserContext(), `
		SELECT id, code, name, city, is_active
		FROM "dz-branches"
		ORDER BY id ASC
	`)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer rows.Close()

	var branches []fiber.Map
	for rows.Next() {
		var id int64
		var code, name, city string
		var active bool
		if err := rows.Scan(&id, &code, &name, &city, &active); err != nil {
			continue
		}
		branches = append(branches, fiber.Map{
			"id":        id,
			"code":      code,
			"name":      name,
			"city":      city,
			"is_active": active,
		})
	}
	return c.JSON(fiber.Map{"branches": branches})
}

func (s *POSService) handleGetMenu(c *fiber.Ctx) error {
	if !s.requirePool(c) {
		return nil
	}
	ctx := c.UserContext()

	catRows, err := s.queries(ctx).Query(ctx, `SELECT id, name, sort_order FROM "dz-menu-categories" ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer catRows.Close()

	type categoryDTO struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	var categories []categoryDTO
	for catRows.Next() {
		var cat categoryDTO
		if err := catRows.Scan(&cat.ID, &cat.Name, &cat.SortOrder); err == nil {
			categories = append(categories, cat)
		}
	}

	// Variants map
	variantMap := make(map[int64][]fiber.Map)
	vRows, err := s.queries(ctx).Query(ctx, `
		SELECT id, menu_item_id, name, price_delta_kurus, recipe_multiplier_pct
		FROM "dz-menu-variants"
		ORDER BY id ASC
	`)
	if err == nil {
		defer vRows.Close()
		for vRows.Next() {
			var id, itemID, delta int64
			var name string
			var mult int
			if err := vRows.Scan(&id, &itemID, &name, &delta, &mult); err == nil {
				variantMap[itemID] = append(variantMap[itemID], fiber.Map{
					"id":                    id,
					"name":                  name,
					"price_delta_kurus":     delta,
					"recipe_multiplier_pct": mult,
				})
			}
		}
	}

	// Modifiers map
	modifierMap := make(map[int64][]fiber.Map)
	mRows, err := s.queries(ctx).Query(ctx, `
		SELECT id, menu_item_id, name, price_delta_kurus, ingredient_code, extra_qty_grams
		FROM "dz-menu-modifiers"
		ORDER BY id ASC
	`)
	if err == nil {
		defer mRows.Close()
		for mRows.Next() {
			var id, itemID, delta, extraGrams int64
			var name, ingCode string
			if err := mRows.Scan(&id, &itemID, &name, &delta, &ingCode, &extraGrams); err == nil {
				modifierMap[itemID] = append(modifierMap[itemID], fiber.Map{
					"id":                id,
					"name":              name,
					"price_delta_kurus": delta,
					"ingredient_code":   ingCode,
					"extra_qty_grams":   extraGrams,
				})
			}
		}
	}

	// Items
	itemRows, err := s.queries(ctx).Query(ctx, `
		SELECT id, category_id, code, name, description, base_price_kurus, station, course, prep_time_sec, is_available
		FROM "dz-menu-items"
		ORDER BY id ASC
	`)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "DB_ERROR", err.Error())
	}
	defer itemRows.Close()

	var items []fiber.Map
	for itemRows.Next() {
		var id, catID, basePrice int64
		var code, name, desc, station string
		var course, prepSec int
		var avail bool
		if err := itemRows.Scan(&id, &catID, &code, &name, &desc, &basePrice, &station, &course, &prepSec, &avail); err == nil {
			vars := variantMap[id]
			if vars == nil {
				vars = []fiber.Map{}
			}
			mods := modifierMap[id]
			if mods == nil {
				mods = []fiber.Map{}
			}
			items = append(items, fiber.Map{
				"id":               id,
				"category_id":      catID,
				"code":             code,
				"name":             name,
				"description":      desc,
				"base_price_kurus": basePrice,
				"station":          station,
				"course":           course,
				"prep_time_sec":    prepSec,
				"is_available":     avail,
				"variants":         vars,
				"modifiers":        mods,
			})
		}
	}

	return c.JSON(fiber.Map{
		"categories": categories,
		"items":      items,
	})
}

func (s *POSService) handleCreateMenuItem(c *fiber.Ctx) error {
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
		CategoryID     int64  `json:"category_id"`
		Code           string `json:"code"`
		Name           string `json:"name"`
		Description    string `json:"description"`
		BasePriceKurus int64  `json:"base_price_kurus"`
		Station        string `json:"station"`
		Course         int    `json:"course"`
		PrepTimeSec    int    `json:"prep_time_sec"`
	}
	if err := c.BodyParser(&req); err != nil {
		return auth.RespondError(c, fiber.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON gövdesi.")
	}
	req.Code = strings.TrimSpace(req.Code)
	req.Name = strings.TrimSpace(req.Name)
	if req.CategoryID <= 0 || req.Code == "" || req.Name == "" || req.BasePriceKurus <= 0 || req.BasePriceKurus > 100000000 || len(req.Code) > 48 || len(req.Name) > 120 {
		return auth.RespondError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "Kategori, ürün kodu, ad ve pozitif fiyat (kuruş) zorunludur.")
	}
	if req.Station == "" {
		req.Station = "hot"
	}
	if req.Course <= 0 {
		req.Course = 2
	}
	if req.PrepTimeSec <= 0 {
		req.PrepTimeSec = 240
	}
	if (req.Station != "cold" && req.Station != "hot" && req.Station != "bar") || req.Course > 10 || req.PrepTimeSec > 86400 {
		return auth.RespondError(c, 400, "VALIDATION_ERROR", "Geçerli istasyon, kurs (1–10) ve hazırlık süresi (1–86400 sn) gerekir.")
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "TX_ERROR", err.Error())
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var itemID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO "dz-menu-items" (category_id, code, name, description, base_price_kurus, station, course, prep_time_sec)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`, req.CategoryID, req.Code, req.Name, req.Description, req.BasePriceKurus, req.Station, req.Course, req.PrepTimeSec).Scan(&itemID)
	if err != nil {
		return auth.RespondError(c, fiber.StatusConflict, "DUPLICATE_OR_INVALID", err.Error())
	}

	// Default variant
	_, _ = tx.Exec(ctx, `
		INSERT INTO "dz-menu-variants" (menu_item_id, name, price_delta_kurus, recipe_multiplier_pct)
		VALUES ($1, 'Standart', 0, 100)
	`, itemID)

	_ = s.writeAuditTx(ctx, tx, claims.BranchID, claims.UserID, claims.Role, "MENU_ITEM_CREATED", "menu_item", fmt.Sprintf("%d", itemID), nil, req)

	if err := tx.Commit(ctx); err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "COMMIT_ERROR", err.Error())
	}

	resp := fiber.Map{
		"id":               itemID,
		"code":             req.Code,
		"name":             req.Name,
		"base_price_kurus": req.BasePriceKurus,
		"station":          req.Station,
		"course":           req.Course,
	}
	return s.saveIdempotencyAndRespond(ctx, c, idempKey, claims.UserID, fiber.StatusCreated, resp)
}
