package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/efehidir/ddo/backend/internal/auth"
	"github.com/efehidir/ddo/backend/internal/config"
	"github.com/efehidir/ddo/backend/internal/database"
	"github.com/efehidir/ddo/backend/internal/domain"
)

// POSService encapsulates database, KDS event broker, and business operations for SofraPOS.
type POSService struct {
	cfg    config.Config
	pg     *database.Postgres
	pool   *pgxpool.Pool
	broker *domain.KDSBroker
}

// NewPOSService creates a POSService instance if a real Postgres pool is provided.
func NewPOSService(cfg config.Config, db database.Probe) *POSService {
	s := &POSService{
		cfg:    cfg,
		broker: domain.NewKDSBroker(1000),
	}
	if pg, ok := db.(*database.Postgres); ok && pg != nil {
		s.pg = pg
		s.pool = pg.Pool()
	}
	return s
}

func (s *POSService) requirePool(c *fiber.Ctx) bool {
	if s.pool == nil {
		_ = auth.RespondError(c, fiber.StatusServiceUnavailable, "DB_UNAVAILABLE", "Veritabanı bağlantısı aktif değil.")
		return false
	}
	return true
}

// checkIdempotency returns true and writes the cached response if the Idempotency-Key was already processed.
func (s *POSService) checkIdempotency(ctx context.Context, c *fiber.Ctx, userID int64) (string, bool) {
	key := strings.TrimSpace(c.Get("Idempotency-Key"))
	if key == "" || s.pool == nil {
		return key, false
	}

	var status int
	var body []byte
	err := s.pool.QueryRow(ctx, `
		SELECT status_code, response_body
		FROM "dz-idempotency-keys"
		WHERE idempotency_key = $1
	`, key).Scan(&status, &body)
	if err == nil && len(body) > 0 {
		c.Set("X-Idempotent-Replay", "true")
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
		_ = c.Status(status).Send(body)
		return key, true
	}
	return key, false
}

func (s *POSService) saveIdempotencyAndRespond(ctx context.Context, c *fiber.Ctx, key string, userID int64, status int, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return auth.RespondError(c, fiber.StatusInternalServerError, "SERIALIZATION_ERROR", err.Error())
	}

	if key != "" && s.pool != nil {
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO "dz-idempotency-keys" (idempotency_key, user_id, method, path, status_code, response_body)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (idempotency_key) DO NOTHING
		`, key, userID, c.Method(), c.Path(), status, raw)
	}

	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(status).Send(raw)
}

func (s *POSService) writeAuditTx(ctx context.Context, tx pgx.Tx, branchID, actorID int64, actorRole, action, entityType, entityID string, oldVal, newVal any) error {
	var oldJSON, newJSON []byte
	if oldVal != nil {
		oldJSON, _ = json.Marshal(oldVal)
	}
	if newVal != nil {
		newJSON, _ = json.Marshal(newVal)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO "dz-audit-logs" (branch_id, actor_id, actor_role, action, entity_type, entity_id, old_value, new_value)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, branchID, actorID, actorRole, action, entityType, entityID, oldJSON, newJSON)
	return err
}

func (s *POSService) appendOrderEventTx(ctx context.Context, tx pgx.Tx, branchID, orderID, actorID int64, eventType string, payload any) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	var eventID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO "dz-order-events" (branch_id, order_id, event_type, actor_id, payload)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, branchID, orderID, eventType, actorID, raw).Scan(&eventID)
	return eventID, err
}

// RegisterPOSRoutes wires all SofraPOS endpoints onto the Fiber app.
func (s *POSService) RegisterPOSRoutes(app *fiber.App) {
	api := app.Group("/api/v1")

	// --- Auth Routes ---
	api.Get("/auth/demo-accounts", s.handleListDemoAccounts)
	api.Post("/auth/login", s.handleLogin)
	api.Post("/auth/refresh", s.handleRefresh)
	api.Get("/auth/me", auth.RequireAuth(s.cfg.JWTSecret), s.handleMe)

	// --- Public / Customer QR Menu & Branches ---
	api.Get("/branches", s.handleListBranches)
	api.Get("/menu", s.handleGetMenu)
	api.Post("/menu/items",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleBranchManager, auth.RoleHQManager),
		s.handleCreateMenuItem,
	)

	// --- Tables & Orders (F-2, T-1) ---
	api.Get("/tables",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleChef, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleListTables,
	)
	api.Get("/orders",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleChef, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleListOrders,
	)
	api.Post("/orders",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleCreateOrder,
	)
	api.Post("/orders/merge",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleMergeOrders,
	)
	api.Get("/orders/:id",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleChef, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleGetOrder,
	)
	api.Get("/orders/:id/rebuild",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleRebuildOrderFromEvents,
	)
	api.Post("/orders/:id/items",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager, auth.RoleCustomer),
		s.handleAddOrderItem,
	)
	api.Post("/orders/:id/items/:itemId/cancel",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleCancelOrderItem,
	)
	api.Post("/orders/:id/move",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleMoveOrderTable,
	)

	// --- Campaigns & Coupons (F-6, T-5) ---
	api.Get("/campaigns",
		auth.RequireAuth(s.cfg.JWTSecret),
		s.handleListCampaigns,
	)
	api.Post("/orders/:id/apply-campaigns",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleApplyCampaigns,
	)

	// --- Split Bill & Payments (F-4, T-2) ---
	api.Post("/orders/:id/split-preview",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleWaiter, auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleSplitPreview,
	)
	api.Post("/orders/:id/payments",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleCreatePayment,
	)

	// --- Kitchen Display System / KDS (F-3, T-3) ---
	api.Get("/kds/items",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleChef, auth.RoleWaiter, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleListKDSItems,
	)
	api.Patch("/kds/items/:itemId/status",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleChef, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleUpdateKDSItemStatus,
	)
	api.Get("/kds/events",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleChef, auth.RoleWaiter, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleReplayKDSEvents,
	)
	api.Get("/kds/stream",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleChef, auth.RoleWaiter, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleKDSStreamSSE,
	)

	// --- Inventory, Recipe Stock & Variance Report (F-5, T-4) ---
	api.Get("/inventory",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleChef, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleGetInventory,
	)
	api.Post("/inventory/count",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleBranchManager, auth.RoleHQManager),
		s.handleSubmitStockCount,
	)
	api.Get("/inventory/variance-report",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleBranchManager, auth.RoleHQManager),
		s.handleGetVarianceReport,
	)

	// --- Shifts, Z-Report, HQ Reports & Audit Logs (F-7) ---
	api.Post("/shifts/open",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleOpenShift,
	)
	api.Post("/shifts/close",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleCloseShift,
	)
	api.Get("/reports/z-report",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleCashier, auth.RoleBranchManager, auth.RoleHQManager),
		s.handleGetZReport,
	)
	api.Get("/reports/hq",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleBranchManager, auth.RoleHQManager),
		s.handleGetHQReport,
	)
	api.Get("/audit-logs",
		auth.RequireAuth(s.cfg.JWTSecret),
		auth.RequireRoles(auth.RoleBranchManager, auth.RoleHQManager),
		s.handleListAuditLogs,
	)

	// --- Demo Reset ---
	api.Post("/admin/reset-demo", s.handleResetDemo)
}
