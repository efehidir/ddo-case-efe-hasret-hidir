package server

import (
	"fmt"
	"github.com/efehidir/ddo/backend/internal/auth"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"strconv"
)

func (s *POSService) handleGetZReport(c *fiber.Ctx) error {
	claims, _ := auth.GetClaims(c)
	branchID := s.resolveBranchID(c, claims)
	ctx := c.UserContext()
	shiftID, err := strconv.ParseInt(c.Query("shift_id", "0"), 10, 64)
	if err != nil || shiftID < 0 {
		return auth.RespondError(c, 400, "INVALID_SHIFT", "Geçersiz vardiya kimliği.")
	}
	// One SQL statement gives a consistent MVCC snapshot of all financial aggregates.
	var status string
	var opening, closing, storedSales, cash, card, tips, cashTips, count, waste, discount int64
	err = s.queries(ctx).QueryRow(ctx, `
	 WITH chosen AS (
	 SELECT * FROM "dz-shifts" WHERE branch_id=$1 AND ($2::bigint=0 OR id=$2) ORDER BY id DESC LIMIT 1
	 ), payments AS (
	 SELECT COALESCE(SUM(amount_kurus) FILTER (WHERE method='CASH'),0) cash,
	 COALESCE(SUM(amount_kurus) FILTER (WHERE method='CARD'),0) card,
	 COALESCE(SUM(tip_kurus),0) tips,
	 COALESCE(SUM(tip_kurus) FILTER (WHERE method='CASH'),0) cash_tips, COUNT(*) count
	 FROM "dz-payments" WHERE shift_id=(SELECT id FROM chosen)
	 )
	 SELECT c.id,c.status,c.opening_cash_kurus,c.closing_cash_kurus,c.total_sales_kurus,
	 p.cash,p.card,p.tips,p.cash_tips,p.count,c.total_waste_kurus,
	 COALESCE((SELECT SUM(o.discount_kurus) FROM "dz-orders" o WHERE o.branch_id=$1 AND o.closed_at>=c.opened_at AND (c.closed_at IS NULL OR o.closed_at<=c.closed_at)),0)
	 FROM chosen c CROSS JOIN payments p`, branchID, shiftID).Scan(&shiftID, &status, &opening, &closing, &storedSales, &cash, &card, &tips, &cashTips, &count, &waste, &discount)
	if err == pgx.ErrNoRows {
		return auth.RespondError(c, 404, "SHIFT_NOT_FOUND", "Vardiya bulunamadı.")
	}
	if err != nil {
		return fmt.Errorf("Z report: %w", err)
	}
	return c.JSON(fiber.Map{
		"branch_id": branchID, "shift_id": shiftID, "shift_status": status,
		"opening_cash_kurus": opening, "closing_cash_kurus": closing,
		"cash_payments_kurus": cash, "card_payments_kurus": card,
		"total_payments_kurus": cash + card, "total_tips_kurus": tips,
		"total_discount_kurus": discount, "total_waste_kurus": waste,
		"grand_total_kurus": cash + card + tips, "payment_count": count,
		"orders_paid_sum_kurus": storedSales, "is_balanced": storedSales == cash+card,
		"expected_cash_kurus": opening + cash + cashTips,
		"cash_variance_kurus": closing - opening - cash - cashTips,
	})
}
