ALTER TABLE "dz-idempotency-keys" ADD COLUMN IF NOT EXISTS request_hash TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_dz_one_open_shift ON "dz-shifts" (branch_id) WHERE status = 'OPEN';
CREATE INDEX IF NOT EXISTS idx_dz_orders_branch_cursor ON "dz-orders" (branch_id, id DESC);
ALTER TABLE "dz-stock-movements" ADD COLUMN IF NOT EXISTS order_item_id BIGINT REFERENCES "dz-order-items"(id);
CREATE INDEX IF NOT EXISTS idx_dz_stock_movements_item ON "dz-stock-movements" (order_item_id);
