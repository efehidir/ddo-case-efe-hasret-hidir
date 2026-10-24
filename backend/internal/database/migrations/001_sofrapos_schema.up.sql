-- ============================================================================
-- SofraPOS — Veritabanı Şeması (PostgreSQL 16)
-- Tüm tablo adları "dz-" öneki ile başlar; kullanıcı tablosu "dz-sk-users".
-- Para tutarları kuruş cinsinden BIGINT olarak tutulur (float yasaktır).
-- ============================================================================

CREATE TABLE IF NOT EXISTS "dz-schema-migrations" (
    version VARCHAR(64) PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS "dz-connection-checks" (
    id BIGSERIAL PRIMARY KEY,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0
);

-- 1. Şubeler (8 Şube)
CREATE TABLE IF NOT EXISTS "dz-branches" (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(32) NOT NULL UNIQUE,
    name VARCHAR(120) NOT NULL,
    city VARCHAR(64) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 2. Kullanıcılar ("dz-sk-users")
CREATE TABLE IF NOT EXISTS "dz-sk-users" (
    id BIGSERIAL PRIMARY KEY,
    branch_id BIGINT NOT NULL REFERENCES "dz-branches"(id) ON DELETE RESTRICT,
    email VARCHAR(160) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    full_name VARCHAR(120) NOT NULL,
    role VARCHAR(32) NOT NULL CHECK (role IN ('waiter', 'cashier', 'chef', 'branch_manager', 'hq_manager', 'customer')),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dz_sk_users_branch_role ON "dz-sk-users"(branch_id, role);

-- 3. Masalar
CREATE TABLE IF NOT EXISTS "dz-tables" (
    id BIGSERIAL PRIMARY KEY,
    branch_id BIGINT NOT NULL REFERENCES "dz-branches"(id) ON DELETE CASCADE,
    code VARCHAR(32) NOT NULL,
    name VARCHAR(64) NOT NULL,
    capacity INT NOT NULL CHECK (capacity > 0),
    status VARCHAR(24) NOT NULL DEFAULT 'EMPTY' CHECK (status IN ('EMPTY', 'OCCUPIED', 'MERGED')),
    active_order_id BIGINT,
    merged_into_table_id BIGINT REFERENCES "dz-tables"(id) ON DELETE SET NULL,
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (branch_id, code)
);

CREATE INDEX IF NOT EXISTS idx_dz_tables_branch_status ON "dz-tables"(branch_id, status);

-- 4. Hammaddeler ve Yarı Mamuller (Alt Reçete Desteği)
CREATE TABLE IF NOT EXISTS "dz-ingredients" (
    id BIGSERIAL PRIMARY KEY,
    branch_id BIGINT NOT NULL REFERENCES "dz-branches"(id) ON DELETE CASCADE,
    code VARCHAR(48) NOT NULL,
    name VARCHAR(120) NOT NULL,
    unit VARCHAR(16) NOT NULL DEFAULT 'g' CHECK (unit IN ('g', 'ml', 'adet')),
    is_semi_finished BOOLEAN NOT NULL DEFAULT FALSE,
    theoretical_qty_grams BIGINT NOT NULL DEFAULT 0,
    cost_per_unit_kurus BIGINT NOT NULL DEFAULT 0 CHECK (cost_per_unit_kurus >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (branch_id, code)
);

-- 5. Menü Kategorileri ve Ürünler
CREATE TABLE IF NOT EXISTS "dz-menu-categories" (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(80) NOT NULL UNIQUE,
    sort_order INT NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS "dz-menu-items" (
    id BIGSERIAL PRIMARY KEY,
    category_id BIGINT NOT NULL REFERENCES "dz-menu-categories"(id) ON DELETE RESTRICT,
    code VARCHAR(48) NOT NULL UNIQUE,
    name VARCHAR(120) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    base_price_kurus BIGINT NOT NULL CHECK (base_price_kurus >= 0),
    station VARCHAR(24) NOT NULL CHECK (station IN ('cold', 'hot', 'bar')),
    course INT NOT NULL DEFAULT 2 CHECK (course IN (1, 2, 3)),
    prep_time_sec INT NOT NULL DEFAULT 300 CHECK (prep_time_sec > 0),
    is_available BOOLEAN NOT NULL DEFAULT TRUE
);

-- 6. Varyantlar (Örn: 1 Porsiyon, 1.5 Porsiyon)
CREATE TABLE IF NOT EXISTS "dz-menu-variants" (
    id BIGSERIAL PRIMARY KEY,
    menu_item_id BIGINT NOT NULL REFERENCES "dz-menu-items"(id) ON DELETE CASCADE,
    name VARCHAR(80) NOT NULL,
    price_delta_kurus BIGINT NOT NULL DEFAULT 0,
    recipe_multiplier_pct INT NOT NULL DEFAULT 100 CHECK (recipe_multiplier_pct > 0),
    UNIQUE (menu_item_id, name)
);

-- 7. Ek Seçenekler / Modifier'lar (Örn: Ekstra Kaşar, Acılı, Soğansız)
CREATE TABLE IF NOT EXISTS "dz-menu-modifiers" (
    id BIGSERIAL PRIMARY KEY,
    menu_item_id BIGINT NOT NULL REFERENCES "dz-menu-items"(id) ON DELETE CASCADE,
    name VARCHAR(80) NOT NULL,
    price_delta_kurus BIGINT NOT NULL DEFAULT 0,
    ingredient_code VARCHAR(48),
    extra_qty_grams BIGINT NOT NULL DEFAULT 0,
    UNIQUE (menu_item_id, name)
);

-- 8. Reçeteler (Ürün -> Hammadde/Yarı Mamul ve Yarı Mamul -> Hammadde)
CREATE TABLE IF NOT EXISTS "dz-recipes" (
    id BIGSERIAL PRIMARY KEY,
    menu_item_id BIGINT REFERENCES "dz-menu-items"(id) ON DELETE CASCADE,
    semi_finished_code VARCHAR(48),
    ingredient_code VARCHAR(48) NOT NULL,
    qty_grams BIGINT NOT NULL CHECK (qty_grams > 0)
);

CREATE INDEX IF NOT EXISTS idx_dz_recipes_menu_item ON "dz-recipes"(menu_item_id);
CREATE INDEX IF NOT EXISTS idx_dz_recipes_semi_code ON "dz-recipes"(semi_finished_code);

-- 9. Adisyonlar / Siparişler
CREATE TABLE IF NOT EXISTS "dz-orders" (
    id BIGSERIAL PRIMARY KEY,
    branch_id BIGINT NOT NULL REFERENCES "dz-branches"(id) ON DELETE RESTRICT,
    table_id BIGINT NOT NULL REFERENCES "dz-tables"(id) ON DELETE RESTRICT,
    status VARCHAR(24) NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'PARTIALLY_PAID', 'PAID', 'MERGED', 'CANCELLED')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    subtotal_kurus BIGINT NOT NULL DEFAULT 0 CHECK (subtotal_kurus >= 0),
    discount_kurus BIGINT NOT NULL DEFAULT 0 CHECK (discount_kurus >= 0),
    total_kurus BIGINT NOT NULL DEFAULT 0 CHECK (total_kurus >= 0),
    paid_kurus BIGINT NOT NULL DEFAULT 0 CHECK (paid_kurus >= 0),
    tip_kurus BIGINT NOT NULL DEFAULT 0 CHECK (tip_kurus >= 0),
    coupon_code VARCHAR(48) NOT NULL DEFAULT '',
    campaign_explanation JSONB NOT NULL DEFAULT '[]'::jsonb,
    opened_by BIGINT REFERENCES "dz-sk-users"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_dz_orders_branch_status_created ON "dz-orders"(branch_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_dz_orders_table_status ON "dz-orders"(table_id, status);

-- 10. Adisyon Kalemleri
CREATE TABLE IF NOT EXISTS "dz-order-items" (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES "dz-orders"(id) ON DELETE CASCADE,
    menu_item_id BIGINT NOT NULL REFERENCES "dz-menu-items"(id) ON DELETE RESTRICT,
    item_name VARCHAR(120) NOT NULL,
    variant_id BIGINT REFERENCES "dz-menu-variants"(id),
    variant_name VARCHAR(80) NOT NULL DEFAULT 'Standart',
    modifier_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    modifier_names TEXT NOT NULL DEFAULT '',
    quantity INT NOT NULL CHECK (quantity > 0),
    unit_price_kurus BIGINT NOT NULL CHECK (unit_price_kurus >= 0),
    line_subtotal_kurus BIGINT NOT NULL CHECK (line_subtotal_kurus >= 0),
    allocated_discount_kurus BIGINT NOT NULL DEFAULT 0 CHECK (allocated_discount_kurus >= 0),
    net_total_kurus BIGINT NOT NULL DEFAULT 0 CHECK (net_total_kurus >= 0),
    paid_kurus BIGINT NOT NULL DEFAULT 0 CHECK (paid_kurus >= 0),
    station VARCHAR(24) NOT NULL CHECK (station IN ('cold', 'hot', 'bar')),
    course INT NOT NULL DEFAULT 2 CHECK (course IN (1, 2, 3)),
    prep_time_sec INT NOT NULL DEFAULT 300,
    kds_status VARCHAR(24) NOT NULL DEFAULT 'PENDING' CHECK (kds_status IN ('PENDING', 'PREPARING', 'READY', 'SERVED', 'CANCELLED')),
    is_produced BOOLEAN NOT NULL DEFAULT FALSE,
    is_cancelled BOOLEAN NOT NULL DEFAULT FALSE,
    cancel_reason TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dz_order_items_order_id ON "dz-order-items"(order_id);
CREATE INDEX IF NOT EXISTS idx_dz_order_items_kds ON "dz-order-items"(station, kds_status, created_at);

-- 11. Adisyon ve Mutfak Olay Günlüğü (Event Sourcing & KDS SSE Replay)
CREATE TABLE IF NOT EXISTS "dz-order-events" (
    id BIGSERIAL PRIMARY KEY,
    branch_id BIGINT NOT NULL REFERENCES "dz-branches"(id) ON DELETE CASCADE,
    order_id BIGINT NOT NULL REFERENCES "dz-orders"(id) ON DELETE CASCADE,
    event_type VARCHAR(48) NOT NULL,
    actor_id BIGINT,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dz_order_events_order_id ON "dz-order-events"(order_id, id ASC);
CREATE INDEX IF NOT EXISTS idx_dz_order_events_branch_id ON "dz-order-events"(branch_id, id ASC);

-- 12. Kampanyalar ve Kuponlar
CREATE TABLE IF NOT EXISTS "dz-campaigns" (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(48) NOT NULL UNIQUE,
    name VARCHAR(120) NOT NULL,
    rule_type VARCHAR(32) NOT NULL CHECK (rule_type IN ('BUY_X_PAY_Y', 'HAPPY_HOUR', 'COMBO')),
    priority INT NOT NULL DEFAULT 10,
    stackable BOOLEAN NOT NULL DEFAULT TRUE,
    config_json JSONB NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS "dz-coupons" (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(48) NOT NULL UNIQUE,
    description VARCHAR(160) NOT NULL,
    discount_type VARCHAR(24) NOT NULL CHECK (discount_type IN ('FIXED', 'PERCENT')),
    discount_value BIGINT NOT NULL CHECK (discount_value > 0),
    min_order_kurus BIGINT NOT NULL DEFAULT 0,
    max_discount_kurus BIGINT NOT NULL DEFAULT 0,
    stackable BOOLEAN NOT NULL DEFAULT TRUE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

-- 13. Vardiyalar ve Z Raporu
CREATE TABLE IF NOT EXISTS "dz-shifts" (
    id BIGSERIAL PRIMARY KEY,
    branch_id BIGINT NOT NULL REFERENCES "dz-branches"(id) ON DELETE RESTRICT,
    opened_by BIGINT REFERENCES "dz-sk-users"(id),
    closed_by BIGINT REFERENCES "dz-sk-users"(id),
    status VARCHAR(16) NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'CLOSED')),
    opening_cash_kurus BIGINT NOT NULL DEFAULT 0,
    closing_cash_kurus BIGINT NOT NULL DEFAULT 0,
    total_sales_kurus BIGINT NOT NULL DEFAULT 0,
    total_discount_kurus BIGINT NOT NULL DEFAULT 0,
    cash_payments_kurus BIGINT NOT NULL DEFAULT 0,
    card_payments_kurus BIGINT NOT NULL DEFAULT 0,
    total_tips_kurus BIGINT NOT NULL DEFAULT 0,
    total_waste_kurus BIGINT NOT NULL DEFAULT 0,
    opened_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_dz_shifts_branch_status ON "dz-shifts"(branch_id, status);

-- 14. Ödemeler (Nakit, Kart, Bölüşme, Bahşiş)
CREATE TABLE IF NOT EXISTS "dz-payments" (
    id BIGSERIAL PRIMARY KEY,
    branch_id BIGINT NOT NULL REFERENCES "dz-branches"(id) ON DELETE RESTRICT,
    order_id BIGINT NOT NULL REFERENCES "dz-orders"(id) ON DELETE RESTRICT,
    shift_id BIGINT REFERENCES "dz-shifts"(id),
    method VARCHAR(16) NOT NULL CHECK (method IN ('CASH', 'CARD')),
    split_mode VARCHAR(24) NOT NULL DEFAULT 'FULL' CHECK (split_mode IN ('FULL', 'EQUAL', 'BY_ITEM', 'CUSTOM', 'MIXED')),
    amount_kurus BIGINT NOT NULL CHECK (amount_kurus > 0),
    tip_kurus BIGINT NOT NULL DEFAULT 0 CHECK (tip_kurus >= 0),
    idempotency_key VARCHAR(128) NOT NULL,
    paid_item_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    cashier_id BIGINT REFERENCES "dz-sk-users"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dz_payments_order_id ON "dz-payments"(order_id);
CREATE INDEX IF NOT EXISTS idx_dz_payments_branch_created ON "dz-payments"(branch_id, created_at DESC);

-- 15. Stok Hareketleri ve Gün Sonu Sayımı
CREATE TABLE IF NOT EXISTS "dz-stock-movements" (
    id BIGSERIAL PRIMARY KEY,
    branch_id BIGINT NOT NULL REFERENCES "dz-branches"(id) ON DELETE CASCADE,
    ingredient_id BIGINT NOT NULL REFERENCES "dz-ingredients"(id) ON DELETE CASCADE,
    order_id BIGINT REFERENCES "dz-orders"(id) ON DELETE SET NULL,
    movement_type VARCHAR(24) NOT NULL CHECK (movement_type IN ('SALE_DEDUCTION', 'WASTE', 'RESTOCK', 'ADJUSTMENT')),
    delta_grams BIGINT NOT NULL,
    cost_kurus BIGINT NOT NULL DEFAULT 0,
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dz_stock_movements_branch_type ON "dz-stock-movements"(branch_id, movement_type, created_at DESC);

CREATE TABLE IF NOT EXISTS "dz-stock-counts" (
    id BIGSERIAL PRIMARY KEY,
    branch_id BIGINT NOT NULL REFERENCES "dz-branches"(id) ON DELETE CASCADE,
    ingredient_id BIGINT NOT NULL REFERENCES "dz-ingredients"(id) ON DELETE CASCADE,
    theoretical_grams BIGINT NOT NULL,
    counted_grams BIGINT NOT NULL,
    variance_grams BIGINT NOT NULL,
    variance_cost_kurus BIGINT NOT NULL,
    counted_by BIGINT REFERENCES "dz-sk-users"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 16. İdempotans Anahtarları
CREATE TABLE IF NOT EXISTS "dz-idempotency-keys" (
    idempotency_key VARCHAR(128) PRIMARY KEY,
    user_id BIGINT,
    method VARCHAR(16) NOT NULL,
    path VARCHAR(255) NOT NULL,
    status_code INT NOT NULL,
    response_body JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 17. Append-Only Denetim Kaydı ("dz-audit-logs")
CREATE TABLE IF NOT EXISTS "dz-audit-logs" (
    id BIGSERIAL PRIMARY KEY,
    branch_id BIGINT,
    actor_id BIGINT,
    actor_role VARCHAR(32) NOT NULL,
    action VARCHAR(64) NOT NULL,
    entity_type VARCHAR(48) NOT NULL,
    entity_id VARCHAR(64) NOT NULL,
    old_value JSONB,
    new_value JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dz_audit_logs_cursor ON "dz-audit-logs"(id DESC);

-- Append-only koruma trigger'ı: UPDATE ve DELETE işlemlerini engeller
CREATE OR REPLACE FUNCTION dz_prevent_audit_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'dz-audit-logs tablosu salt-eklemeli (append-only) bir tablodur; UPDATE veya DELETE yapılamaz.';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_dz_audit_logs_immutable ON "dz-audit-logs";
CREATE TRIGGER trg_dz_audit_logs_immutable
BEFORE UPDATE OR DELETE ON "dz-audit-logs"
FOR EACH ROW EXECUTE FUNCTION dz_prevent_audit_mutation();
