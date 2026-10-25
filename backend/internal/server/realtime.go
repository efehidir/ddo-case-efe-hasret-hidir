package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/efehidir/ddo/backend/internal/auth"
	"github.com/efehidir/ddo/backend/internal/domain"
	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"
)

// Persisted events are the source for replay and live delivery. Polling avoids
// a replay/subscribe gap and continues to work across API restarts.
func (s *POSService) readKDSBatch(ctx context.Context, branchID, afterID int64) ([]domain.KDSStreamEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, branch_id, order_id, event_type, COALESCE(payload->>'station',''), created_at FROM "dz-order-events" WHERE branch_id=$1 AND id>$2 ORDER BY id LIMIT 200`, branchID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]domain.KDSStreamEvent, 0)
	for rows.Next() {
		var ev domain.KDSStreamEvent
		if err := rows.Scan(&ev.EventID, &ev.BranchID, &ev.OrderID, &ev.EventType, &ev.Station, &ev.CreatedAt); err != nil {
			return nil, err
		}
		ev.Summary = ev.EventType
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (s *POSService) handleKDSStreamSSE(c *fiber.Ctx) error {
	claims, _ := auth.GetClaims(c)
	branchID := s.resolveBranchID(c, claims)
	rawCursor := c.Query("since_event_id", "0")
	if header := c.Get("Last-Event-ID"); header != "" {
		rawCursor = header
	}
	cursor, err := strconv.ParseInt(rawCursor, 10, 64)
	if err != nil || cursor < 0 {
		return auth.RespondError(c, 400, "INVALID_CURSOR", "Geçersiz mutfak olay kimliği.")
	}
	first, err := s.readKDSBatch(c.UserContext(), branchID, cursor)
	if err != nil {
		return err
	}
	once := c.Query("once") == "1"
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache, no-transform")
	c.Set("X-Accel-Buffering", "no")
	c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
		_, _ = fmt.Fprint(w, "retry: 1500\n\n")
		deadline := time.Now().Add(25 * time.Second)
		batch := first
		for {
			for _, ev := range batch {
				raw, _ := json.Marshal(ev)
				if _, err := fmt.Fprintf(w, "id: %d\nevent: kds_event\ndata: %s\n\n", ev.EventID, raw); err != nil {
					return
				}
				cursor = ev.EventID
			}
			if err := w.Flush(); err != nil {
				return
			}
			if len(batch) < 200 {
				if once || time.Now().After(deadline) {
					return
				}
				time.Sleep(time.Second)
				if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			var err error
			batch, err = s.readKDSBatch(ctx, branchID, cursor)
			cancel()
			if err != nil {
				return
			}
		}
	}))
	return nil
}
