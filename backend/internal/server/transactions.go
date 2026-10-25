package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/efehidir/ddo/backend/internal/auth"
)

type requestTxKey struct{}

type querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *POSService) queries(ctx context.Context) querier {
	if tx, ok := ctx.Value(requestTxKey{}).(pgx.Tx); ok {
		return tx
	}
	return s.pool
}

func (s *POSService) beginTx(ctx context.Context) (pgx.Tx, error) {
	if tx, ok := ctx.Value(requestTxKey{}).(pgx.Tx); ok {
		return tx.Begin(ctx) // Handler transactions become savepoints in the request transaction.
	}
	return s.pool.Begin(ctx)
}

func canAccessBranch(claims auth.Claims, branchID int64) bool {
	return claims.Role == auth.RoleHQManager || (branchID > 0 && claims.BranchID == branchID)
}

// guard checks branch ownership for IDs as well as explicit query/body branch selection.
// Role checks run before this wrapper in the route declaration.
func (s *POSService) guard(next fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !s.requirePool(c) {
			return nil
		}
		claims, _ := auth.GetClaims(c)
		if raw := c.Query("branch_id"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return auth.RespondError(c, 400, "INVALID_BRANCH", "Geçerli şube kimliği zorunludur.")
			}
			if !canAccessBranch(claims, id) {
				return auth.RespondError(c, 403, "BRANCH_FORBIDDEN", "Bu şubeye erişim yetkiniz yok.")
			}
		}
		var body struct {
			BranchID      int64 `json:"branch_id"`
			TableID       int64 `json:"table_id"`
			TargetTableID int64 `json:"target_table_id"`
			SourceOrderID int64 `json:"source_order_id"`
			TargetOrderID int64 `json:"target_order_id"`
		}
		if len(c.Body()) > 0 {
			if err := c.BodyParser(&body); err != nil {
				return auth.RespondError(c, 400, "INVALID_JSON", "Geçersiz JSON gövdesi.")
			}
			if body.BranchID < 0 || (body.BranchID > 0 && !canAccessBranch(claims, body.BranchID)) {
				return auth.RespondError(c, 403, "BRANCH_FORBIDDEN", "Bu şubeye erişim yetkiniz yok.")
			}
		}
		type reference struct {
			id    int64
			query string
		}
		refs := []reference{
			{body.TableID, `SELECT branch_id FROM "dz-tables" WHERE id=$1`},
			{body.TargetTableID, `SELECT branch_id FROM "dz-tables" WHERE id=$1`},
			{body.SourceOrderID, `SELECT branch_id FROM "dz-orders" WHERE id=$1`},
			{body.TargetOrderID, `SELECT branch_id FROM "dz-orders" WHERE id=$1`},
		}
		if raw := c.Params("id"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return auth.RespondError(c, 400, "INVALID_ID", "Geçersiz adisyon kimliği.")
			}
			refs = append(refs, reference{id, `SELECT branch_id FROM "dz-orders" WHERE id=$1`})
		}
		if raw := c.Params("itemId"); raw != "" && strings.Contains(c.Path(), "/kds/") {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return auth.RespondError(c, 400, "INVALID_ID", "Geçersiz kalem kimliği.")
			}
			refs = append(refs, reference{id, `SELECT o.branch_id FROM "dz-order-items" i JOIN "dz-orders" o ON o.id=i.order_id WHERE i.id=$1`})
		}
		for _, ref := range refs {
			if ref.id == 0 {
				continue
			}
			var branchID int64
			if err := s.queries(c.UserContext()).QueryRow(c.UserContext(), ref.query, ref.id).Scan(&branchID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return auth.RespondError(c, 404, "NOT_FOUND", "Kayıt bulunamadı.")
				}
				return err
			}
			if !canAccessBranch(claims, branchID) {
				return auth.RespondError(c, 403, "BRANCH_FORBIDDEN", "Bu kaydın şubesine erişim yetkiniz yok.")
			}
			c.Locals("mutation_branch", branchID)
		}
		if c.Locals("mutation_branch") == nil {
			branchID := s.resolveBranchID(c, claims)
			if body.BranchID > 0 {
				branchID = body.BranchID
			}
			c.Locals("mutation_branch", branchID)
		}
		return next(c)
	}
}

func requestIdentity(userID int64, method, path, key string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s", userID, method, path, key)))
	return hex.EncodeToString(sum[:])
}

// atomic commits business data, events, audit records and the original HTTP response together.
// Branch advisory locks also order event commits, so an SSE cursor cannot skip a late commit.
func (s *POSService) atomic(next fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		claims, _ := auth.GetClaims(c)
		key := strings.TrimSpace(c.Get("Idempotency-Key"))
		if len(key) > 100 {
			return auth.RespondError(c, 400, "INVALID_IDEMPOTENCY_KEY", "İdempotans anahtarı en fazla 100 karakter olabilir.")
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(6433, 0)`); err != nil {
			return err
		}
		var identity, fingerprint string
		if key != "" {
			identity = requestIdentity(claims.UserID, c.Method(), c.Path(), key)
			sum := sha256.Sum256(append([]byte(c.OriginalURL()+"\x00"), c.Body()...))
			fingerprint = hex.EncodeToString(sum[:])
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, identity); err != nil {
				return err
			}
			var status int
			var raw []byte
			var savedFingerprint string
			err = tx.QueryRow(ctx, `SELECT status_code, response_body, request_hash FROM "dz-idempotency-keys" WHERE idempotency_key=$1`, identity).Scan(&status, &raw, &savedFingerprint)
			if err == nil {
				if fingerprint != savedFingerprint {
					return auth.RespondError(c, 409, "IDEMPOTENCY_CONFLICT", "Aynı anahtar farklı bir istek gövdesiyle kullanılamaz.")
				}
				c.Set("X-Idempotent-Replay", "true")
				c.Type("json")
				return c.Status(status).Send(raw)
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		branchID, _ := c.Locals("mutation_branch").(int64)
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(6434, $1::int)`, branchID); err != nil {
			return err
		}
		c.SetUserContext(context.WithValue(ctx, requestTxKey{}, tx))
		defer c.SetUserContext(ctx)
		if err = next(c); err != nil {
			return err
		}
		status := c.Response().StatusCode()
		if status >= 400 {
			return nil
		}
		if key != "" {
			_, err = tx.Exec(ctx, `INSERT INTO "dz-idempotency-keys" (idempotency_key,user_id,method,path,status_code,response_body,request_hash) VALUES ($1,$2,$3,$4,$5,$6,$7)`, identity, claims.UserID, c.Method(), c.Path(), status, []byte(c.Response().Body()), fingerprint)
			if err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
}
