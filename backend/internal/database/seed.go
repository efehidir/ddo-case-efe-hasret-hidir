package database

import (
	"context"
	_ "embed"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

//go:embed migrations/001_sofrapos_schema.up.sql
var migration001UpSQL string

//go:embed migrations/001_sofrapos_schema.down.sql
var migration001DownSQL string

//go:embed migrations/002_integrity.up.sql
var migration002UpSQL string

// DefaultDemoPassword is the password for all seeded demo accounts.
const DefaultDemoPassword = "Sofra2026!"

// MigrateAndSeed applies SQL migrations and ensures initial deterministic seed data exists.
func (p *Postgres) MigrateAndSeed(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx, migration001UpSQL); err != nil {
		return fmt.Errorf("apply migration 001: %w", err)
	}

	_, err := p.pool.Exec(ctx, `
		INSERT INTO "dz-schema-migrations" (version)
		VALUES ('001_sofrapos_schema')
		ON CONFLICT (version) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("record migration version: %w", err)
	}

	if _, err := p.pool.Exec(ctx, migration002UpSQL); err != nil {
		return fmt.Errorf("apply migration 002: %w", err)
	}
	if _, err := p.pool.Exec(ctx, `INSERT INTO "dz-schema-migrations" (version) VALUES ('002_integrity') ON CONFLICT DO NOTHING`); err != nil {
		return err
	}

	var userCount int
	if err := p.pool.QueryRow(ctx, `SELECT COUNT(*) FROM "dz-sk-users"`).Scan(&userCount); err != nil {
		return fmt.Errorf("check user count: %w", err)
	}

	if userCount == 0 {
		if err := p.seedInitialData(ctx); err != nil {
			return fmt.Errorf("seed initial data: %w", err)
		}
	}

	return nil
}

// ResetDemoData drops and recreates the schema and re-runs the deterministic initial seed.
func (p *Postgres) ResetDemoData(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx, migration001DownSQL); err != nil {
		return fmt.Errorf("drop schema during reset: %w", err)
	}
	return p.MigrateAndSeed(ctx)
}

func (p *Postgres) seedInitialData(ctx context.Context) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Seed 8 Branches
	branches := []struct {
		code string
		name string
		city string
	}{
		{"BR-01", "Kadıköy Merkez Şube", "İstanbul"},
		{"BR-02", "Beşiktaş Sahil Şube", "İstanbul"},
		{"BR-03", "Çankaya Tunalı Şube", "Ankara"},
		{"BR-04", "Alsancak Kordon Şube", "İzmir"},
		{"BR-05", "Nilüfer FSM Şube", "Bursa"},
		{"BR-06", "Lara Sahil Şube", "Antalya"},
		{"BR-07", "Seyhan Ziyapaşa Şube", "Adana"},
		{"BR-08", "Şehitkamil İncilipınar Şube", "Gaziantep"},
	}

	for _, b := range branches {
		_, err := tx.Exec(ctx, `
			INSERT INTO "dz-branches" (code, name, city)
			VALUES ($1, $2, $3)
		`, b.code, b.name, b.city)
		if err != nil {
			return fmt.Errorf("insert branch %s: %w", b.code, err)
		}
	}

	// 2. Seed Users into "dz-sk-users"
	// First user MUST be seed.ceylan@ddo-case.invalid
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(DefaultDemoPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	passHash := string(hashBytes)

	users := []struct {
		branchID int64
		email    string
		fullName string
		role     string
	}{
		{1, "seed.ceylan@ddo-case.invalid", "Ceylan Yılmaz (Seed Yönetici)", "hq_manager"},
		{1, "garson@sofrapos.local", "Ahmet Demir (Garson)", "waiter"},
		{1, "kasiyer@sofrapos.local", "Zeynep Kaya (Kasiyer)", "cashier"},
		{1, "asci@sofrapos.local", "Mehmet Usta (Aşçı - KDS)", "chef"},
		{1, "mudur@sofrapos.local", "Selin Arslan (Şube Müdürü)", "branch_manager"},
		{1, "merkez@sofrapos.local", "Burak Çelik (Merkez Yönetim)", "hq_manager"},
		{1, "musteri@sofrapos.local", "Elif Şahin (QR Müşteri)", "customer"},
	}

	for _, u := range users {
		_, err := tx.Exec(ctx, `
			INSERT INTO "dz-sk-users" (branch_id, email, password_hash, full_name, role)
			VALUES ($1, $2, $3, $4, $5)
		`, u.branchID, u.email, passHash, u.fullName, u.role)
		if err != nil {
			return fmt.Errorf("insert user %s: %w", u.email, err)
		}
	}

	// 3. Seed Tables & Ingredients & Open Shifts for all 8 branches
	ingredients := []struct {
		code           string
		name           string
		unit           string
		isSemiFinished bool
		initialGrams   int64
		costPerUnit    int64
	}{
		{"ING-BEEF", "Yaprak Dana Döner Eti", "g", false, 50000, 65},
		{"ING-LAMB-MINCE", "Zırh Kuzu Kıyma", "g", false, 40000, 55},
		{"ING-DOUGH", "Taş Fırın Hamuru", "g", false, 60000, 12},
		{"ING-TOMATO", "Çanakkale Domates", "g", false, 50000, 8},
		{"ING-BUTTER", "Trabzon Tereyağı", "g", false, 25000, 48},
		{"ING-CHEESE", "Kars Eski Kaşar", "g", false, 30000, 42},
		{"ING-YOGURT", "Tava Süzme Yoğurt", "g", false, 45000, 18},
		{"ING-PISTACHIO", "Boz Antep Fıstığı İçi", "g", false, 15000, 140},
		{"SEMI-ISKENDER-SAUCE", "Özel İskender Domates Sosu (Yarı Mamul)", "g", true, 20000, 16},
		{"SEMI-LAHMACUN-HARC", "Zırh Lahmacun İç Harcı (Yarı Mamul)", "g", true, 25000, 40},
	}

	for branchID := int64(1); branchID <= 8; branchID++ {
		for t := 1; t <= 10; t++ {
			code := fmt.Sprintf("T-%02d", t)
			name := fmt.Sprintf("Masa %d", t)
			capacity := 4
			if t >= 8 {
				capacity = 6
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO "dz-tables" (branch_id, code, name, capacity, status)
				VALUES ($1, $2, $3, $4, 'EMPTY')
			`, branchID, code, name, capacity)
			if err != nil {
				return fmt.Errorf("insert table: %w", err)
			}
		}

		for _, ing := range ingredients {
			_, err := tx.Exec(ctx, `
				INSERT INTO "dz-ingredients" (branch_id, code, name, unit, is_semi_finished, theoretical_qty_grams, cost_per_unit_kurus)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, branchID, ing.code, ing.name, ing.unit, ing.isSemiFinished, ing.initialGrams, ing.costPerUnit)
			if err != nil {
				return fmt.Errorf("insert ingredient %s: %w", ing.code, err)
			}
		}

		// Open shift for each branch
		_, err := tx.Exec(ctx, `
			INSERT INTO "dz-shifts" (branch_id, opened_by, status, opening_cash_kurus)
			VALUES ($1, 3, 'OPEN', 500000)
		`, branchID)
		if err != nil {
			return fmt.Errorf("insert shift for branch %d: %w", branchID, err)
		}
	}

	// 4. Seed Menu Categories & Menu Items
	categories := []struct {
		name      string
		sortOrder int
	}{
		{"Başlangıç ve Soğuk Mezeler", 1},
		{"Ana Yemek ve Kebaplar", 2},
		{"İçecek ve Tatlı Barı", 3},
	}

	for _, c := range categories {
		if _, err := tx.Exec(ctx, `INSERT INTO "dz-menu-categories" (name, sort_order) VALUES ($1, $2)`, c.name, c.sortOrder); err != nil {
			return err
		}
	}

	menuItems := []struct {
		catID       int64
		code        string
		name        string
		desc        string
		basePrice   int64
		station     string
		course      int
		prepTimeSec int
	}{
		{2, "MN-ISKENDER", "Bursa İskender Kebap", "Odun ateşinde yaprak döner, özel domates sosu, süzme yoğurt ve kızgın Trabzon tereyağı ile", 36000, "hot", 2, 420},
		{2, "MN-LAHMACUN", "Antep Taş Fırın Lahmacun", "Zırh kıyması ve taze sebzeli çıtır taş fırın lahmacun", 11000, "hot", 2, 240},
		{1, "MN-HAYDARI", "Atom Süzme Haydari", "Tereyağında kızdırılmış Arnavut biberli süzme yoğurt mezesi", 9500, "cold", 1, 120},
		{3, "MN-AYRAN", "Bakır Maşrapa Yayık Ayran", "Bol köpüklü doğal yayık ayranı", 4500, "bar", 2, 60},
		{3, "MN-KUNEFE", "Boz Fıstıklı Taş Kadayıf Künefe", "Özel tuzsuz peynirli ve bol Antep fıstıklı sıcak künefe", 19000, "bar", 3, 360},
	}

	for _, m := range menuItems {
		if _, err := tx.Exec(ctx, `
			INSERT INTO "dz-menu-items" (category_id, code, name, description, base_price_kurus, station, course, prep_time_sec)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, m.catID, m.code, m.name, m.desc, m.basePrice, m.station, m.course, m.prepTimeSec); err != nil {
			return err
		}
	}

	// 5. Seed Variants
	variants := []struct {
		menuItemID   int64
		name         string
		priceDelta   int64
		multiplierPc int
	}{
		{1, "1 Porsiyon", 0, 100},
		{1, "1.5 Porsiyon", 16000, 150},
		{1, "Duble Porsiyon", 30000, 200},
		{2, "Standart", 0, 100},
		{2, "Fındık Lahmacun", -3500, 60},
		{3, "Standart", 0, 100},
		{3, "Büyük Porsiyon", 4000, 150},
		{4, "Bakır Maşrapa", 0, 100},
		{4, "Büyük Maşrapa", 2000, 150},
		{5, "Tek Kişilik", 0, 100},
		{5, "İki Kişilik", 15000, 180},
	}
	for _, v := range variants {
		if _, err := tx.Exec(ctx, `
			INSERT INTO "dz-menu-variants" (menu_item_id, name, price_delta_kurus, recipe_multiplier_pct)
			VALUES ($1, $2, $3, $4)
		`, v.menuItemID, v.name, v.priceDelta, v.multiplierPc); err != nil {
			return err
		}
	}

	// 6. Seed Modifiers
	modifiers := []struct {
		menuItemID     int64
		name           string
		priceDelta     int64
		ingredientCode string
		extraQtyGrams  int64
	}{
		{1, "Ekstra Tereyağı", 4000, "ING-BUTTER", 30},
		{1, "Ekstra Kaşar", 4500, "ING-CHEESE", 40},
		{1, "Yoğurtsuz", 0, "", 0},
		{2, "Acılı", 0, "", 0},
		{2, "Soğansız", 0, "", 0},
		{2, "Ekstra Kaşarlı", 3000, "ING-CHEESE", 30},
		{5, "Ekstra Antep Fıstığı", 6000, "ING-PISTACHIO", 25},
	}
	for _, mod := range modifiers {
		if _, err := tx.Exec(ctx, `
			INSERT INTO "dz-menu-modifiers" (menu_item_id, name, price_delta_kurus, ingredient_code, extra_qty_grams)
			VALUES ($1, $2, $3, $4, $5)
		`, mod.menuItemID, mod.name, mod.priceDelta, mod.ingredientCode, mod.extraQtyGrams); err != nil {
			return err
		}
	}

	// 7. Seed Recipes (including semi-finished sub-recipes!)
	// Sub-recipe 1: SEMI-ISKENDER-SAUCE (150g batch = 120g tomato + 30g butter)
	// Sub-recipe 2: SEMI-LAHMACUN-HARC (130g batch = 90g lamb mince + 40g tomato)
	_, err = tx.Exec(ctx, `
		INSERT INTO "dz-recipes" (menu_item_id, semi_finished_code, ingredient_code, qty_grams) VALUES
		-- Sub-recipes (yarı mamul alt reçeteleri)
		(NULL, 'SEMI-ISKENDER-SAUCE', 'ING-TOMATO', 120),
		(NULL, 'SEMI-ISKENDER-SAUCE', 'ING-BUTTER', 30),
		(NULL, 'SEMI-LAHMACUN-HARC', 'ING-LAMB-MINCE', 90),
		(NULL, 'SEMI-LAHMACUN-HARC', 'ING-TOMATO', 40),
		-- Menu Item 1: Bursa İskender Kebap (uses SEMI-ISKENDER-SAUCE)
		(1, NULL, 'ING-BEEF', 180),
		(1, NULL, 'SEMI-ISKENDER-SAUCE', 150),
		(1, NULL, 'ING-YOGURT', 80),
		-- Menu Item 2: Antep Lahmacun (uses SEMI-LAHMACUN-HARC)
		(2, NULL, 'ING-DOUGH', 100),
		(2, NULL, 'SEMI-LAHMACUN-HARC', 130),
		-- Menu Item 3: Atom Haydari
		(3, NULL, 'ING-YOGURT', 150),
		(3, NULL, 'ING-BUTTER', 20),
		-- Menu Item 4: Yayık Ayran
		(4, NULL, 'ING-YOGURT', 120),
		-- Menu Item 5: Künefe
		(5, NULL, 'ING-CHEESE', 60),
		(5, NULL, 'ING-PISTACHIO', 35),
		(5, NULL, 'ING-BUTTER', 30)
	`)
	if err != nil {
		return fmt.Errorf("insert recipes: %w", err)
	}

	// 8. Seed Campaigns & Coupons
	_, err = tx.Exec(ctx, `
		INSERT INTO "dz-campaigns" (code, name, rule_type, priority, stackable, config_json, is_active) VALUES
		('CMP-3AL2ODE', '3 Al 2 Öde Taş Fırın Lahmacun', 'BUY_X_PAY_Y', 20, TRUE, '{"item_code":"MN-LAHMACUN","buy_qty":3,"pay_qty":2}', TRUE),
		('CMP-COMBO-KEBAP', 'İskender + Ayran + Künefe Ziyafet Menüsü (85 TL İndirim)', 'COMBO', 30, TRUE, '{"required_codes":["MN-ISKENDER","MN-AYRAN","MN-KUNEFE"],"discount_kurus":8500}', TRUE),
		('CMP-HAPPY-HOUR', 'Akşam Servisi Bereket İndirimi (%10)', 'HAPPY_HOUR', 10, FALSE, '{"discount_pct":10,"start_hour":0,"end_hour":24}', TRUE)
	`)
	if err != nil {
		return fmt.Errorf("insert campaigns: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO "dz-coupons" (code, description, discount_type, discount_value, min_order_kurus, max_discount_kurus, stackable, is_active) VALUES
		('SOFRA50', '300 TL ve üzeri siparişlerde 50 TL indirim (Birleştirilebilir)', 'FIXED', 5000, 30000, 5000, TRUE, TRUE),
		('VIP20', '500 TL ve üzeri siparişlerde %20 VIP indirim (Tekil Kullanım)', 'PERCENT', 20, 50000, 25000, FALSE, TRUE)
	`)
	if err != nil {
		return fmt.Errorf("insert coupons: %w", err)
	}

	// 9. Initial Audit Entry
	_, err = tx.Exec(ctx, `
		INSERT INTO "dz-audit-logs" (branch_id, actor_id, actor_role, action, entity_type, entity_id, old_value, new_value)
		VALUES (1, 1, 'hq_manager', 'SYSTEM_SEED_COMPLETED', 'system', '8-branches', NULL, '{"branches":8,"users":7}'::jsonb)
	`)
	if err != nil {
		return fmt.Errorf("insert initial audit log: %w", err)
	}

	return tx.Commit(ctx)
}
