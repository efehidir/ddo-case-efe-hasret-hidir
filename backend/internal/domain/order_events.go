package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	EventOrderOpened     = "ORDER_OPENED"
	EventItemAdded       = "ITEM_ADDED"
	EventItemStatus      = "ITEM_KDS_STATUS_CHANGED"
	EventItemCancelled   = "ITEM_CANCELLED"
	EventTableMoved      = "TABLE_MOVED"
	EventTableMerged     = "TABLE_MERGED"
	EventCampaignApplied = "CAMPAIGN_APPLIED"
	EventPaymentReceived = "PAYMENT_RECEIVED"
)

// OrderEvent represents an immutable event in "dz-order-events".
type OrderEvent struct {
	ID        int64           `json:"id"`
	BranchID  int64           `json:"branch_id"`
	OrderID   int64           `json:"order_id"`
	EventType string          `json:"event_type"`
	ActorID   int64           `json:"actor_id"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// ItemAddedPayload is recorded when a waiter or merged table adds an item.
type ItemAddedPayload struct {
	OrderItemID       int64  `json:"order_item_id"`
	MenuItemID        int64  `json:"menu_item_id"`
	ItemName          string `json:"item_name"`
	VariantName       string `json:"variant_name"`
	ModifierNames     string `json:"modifier_names"`
	Quantity          int    `json:"quantity"`
	UnitPriceKurus    int64  `json:"unit_price_kurus"`
	LineSubtotalKurus int64  `json:"line_subtotal_kurus"`
	Station           string `json:"station"`
	Course            int    `json:"course"`
}

// ItemCancelledPayload is recorded when an item is cancelled (with or without waste/fire).
type ItemCancelledPayload struct {
	OrderItemID       int64  `json:"order_item_id"`
	LineSubtotalKurus int64  `json:"line_subtotal_kurus"`
	IsProduced        bool   `json:"is_produced"`
	WasteRecorded     bool   `json:"waste_recorded"`
	Reason            string `json:"reason"`
}

// TableMovedPayload is recorded when an order is moved from one table to another.
type TableMovedPayload struct {
	FromTableID int64 `json:"from_table_id"`
	ToTableID   int64 `json:"to_table_id"`
}

// TableMergedPayload is recorded when a source order/table is merged into a target order.
type TableMergedPayload struct {
	SourceOrderID         int64 `json:"source_order_id"`
	TargetOrderID         int64 `json:"target_order_id"`
	TransferredTotalKurus int64 `json:"transferred_total_kurus"`
}

// CampaignAppliedPayload is recorded when discounts/coupons are applied to an order.
type CampaignAppliedPayload struct {
	DiscountKurus int64  `json:"discount_kurus"`
	CouponCode    string `json:"coupon_code"`
}

// PaymentReceivedPayload is recorded when a payment (partial or full) is received.
type PaymentReceivedPayload struct {
	PaymentID   int64  `json:"payment_id"`
	Method      string `json:"method"`
	SplitMode   string `json:"split_mode"`
	AmountKurus int64  `json:"amount_kurus"`
	TipKurus    int64  `json:"tip_kurus"`
}

// RebuiltOrderState represents the deterministic state reconstructed purely from OrderEvent log.
type RebuiltOrderState struct {
	OrderID         int64   `json:"order_id"`
	CurrentTableID  int64   `json:"current_table_id"`
	ActiveItemCount int     `json:"active_item_count"`
	CancelledCount  int     `json:"cancelled_count"`
	WasteItemCount  int     `json:"waste_item_count"`
	MergedOrders    []int64 `json:"merged_orders"`
	SubtotalKurus   int64   `json:"subtotal_kurus"`
	DiscountKurus   int64   `json:"discount_kurus"`
	TotalKurus      int64   `json:"total_kurus"`
	PaidKurus       int64   `json:"paid_kurus"`
	TipKurus        int64   `json:"tip_kurus"`
	RemainingKurus  int64   `json:"remaining_kurus"`
	Status          string  `json:"status"`
	EventsProcessed int     `json:"events_processed"`
}

// RebuildOrderStateFromEvents replays the event log in order to reconstruct exact kurus totals.
func RebuildOrderStateFromEvents(events []OrderEvent) (RebuiltOrderState, error) {
	if len(events) == 0 {
		return RebuiltOrderState{}, errors.New("cannot rebuild order from empty event log")
	}

	state := RebuiltOrderState{
		OrderID:      events[0].OrderID,
		MergedOrders: []int64{},
		Status:       "OPEN",
	}

	itemSubtotals := make(map[int64]int64)
	cancelledItems := make(map[int64]bool)

	for _, ev := range events {
		state.EventsProcessed++
		switch ev.EventType {
		case EventOrderOpened:
			var p struct {
				TableID int64 `json:"table_id"`
			}
			_ = json.Unmarshal(ev.Payload, &p)
			if p.TableID > 0 {
				state.CurrentTableID = p.TableID
			}

		case EventItemAdded:
			var p ItemAddedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return RebuiltOrderState{}, fmt.Errorf("unmarshal ITEM_ADDED: %w", err)
			}
			itemSubtotals[p.OrderItemID] = p.LineSubtotalKurus
			state.SubtotalKurus += p.LineSubtotalKurus
			state.ActiveItemCount++

		case EventItemCancelled:
			var p ItemCancelledPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return RebuiltOrderState{}, fmt.Errorf("unmarshal ITEM_CANCELLED: %w", err)
			}
			if !cancelledItems[p.OrderItemID] {
				cancelledItems[p.OrderItemID] = true
				sub := p.LineSubtotalKurus
				if recorded, ok := itemSubtotals[p.OrderItemID]; ok && recorded > 0 {
					sub = recorded
				}
				state.SubtotalKurus -= sub
				if state.SubtotalKurus < 0 {
					state.SubtotalKurus = 0
				}
				if state.ActiveItemCount > 0 {
					state.ActiveItemCount--
				}
				state.CancelledCount++
				if p.IsProduced || p.WasteRecorded {
					state.WasteItemCount++
				}
			}

		case EventTableMoved:
			var p TableMovedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return RebuiltOrderState{}, fmt.Errorf("unmarshal TABLE_MOVED: %w", err)
			}
			state.CurrentTableID = p.ToTableID

		case EventTableMerged:
			var p TableMergedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return RebuiltOrderState{}, fmt.Errorf("unmarshal TABLE_MERGED: %w", err)
			}
			if state.OrderID == p.SourceOrderID {
				state.SubtotalKurus, state.DiscountKurus, state.TotalKurus = 0, 0, 0
				state.ActiveItemCount = 0
				state.Status = "MERGED"
			} else {
				state.MergedOrders = append(state.MergedOrders, p.SourceOrderID)
			}

		case EventCampaignApplied:
			var p CampaignAppliedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return RebuiltOrderState{}, fmt.Errorf("unmarshal CAMPAIGN_APPLIED: %w", err)
			}
			state.DiscountKurus = p.DiscountKurus

		case EventPaymentReceived:
			var p PaymentReceivedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return RebuiltOrderState{}, fmt.Errorf("unmarshal PAYMENT_RECEIVED: %w", err)
			}
			state.PaidKurus += p.AmountKurus
			state.TipKurus += p.TipKurus
		}
	}

	if state.DiscountKurus > state.SubtotalKurus {
		state.DiscountKurus = state.SubtotalKurus
	}
	state.TotalKurus = state.SubtotalKurus - state.DiscountKurus
	state.RemainingKurus = state.TotalKurus - state.PaidKurus
	if state.RemainingKurus < 0 {
		state.RemainingKurus = 0
	}

	if state.Status == "MERGED" {
		return state, nil
	}
	if state.TotalKurus > 0 && state.PaidKurus >= state.TotalKurus {
		state.Status = "PAID"
	} else if state.PaidKurus > 0 {
		state.Status = "PARTIALLY_PAID"
	} else {
		state.Status = "OPEN"
	}

	return state, nil
}

// OptimisticOrderStore is an in-memory concurrency harness used to verify optimistic locking retry semantics.
type OptimisticOrderStore struct {
	mu      sync.Mutex
	version int64
	events  []OrderEvent
	nextID  int64
}

// NewOptimisticOrderStore creates an order store with version 1 and an initial ORDER_OPENED event.
func NewOptimisticOrderStore(orderID, tableID int64) *OptimisticOrderStore {
	openPayload, _ := json.Marshal(map[string]int64{"table_id": tableID})
	return &OptimisticOrderStore{
		version: 1,
		nextID:  2,
		events: []OrderEvent{
			{
				ID:        1,
				BranchID:  1,
				OrderID:   orderID,
				EventType: EventOrderOpened,
				Payload:   openPayload,
				CreatedAt: time.Now().UTC(),
			},
		},
	}
}

// ErrOptimisticConflict is returned when the caller's expected version does not match the current version.
var ErrOptimisticConflict = errors.New("optimistic locking version conflict")

// ReadVersion returns the current order version.
func (s *OptimisticOrderStore) ReadVersion() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// AppendEventWithVersion appends an event only if expectedVersion matches current version, incrementing version.
func (s *OptimisticOrderStore) AppendEventWithVersion(expectedVersion int64, eventType string, payload any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.version != expectedVersion {
		return ErrOptimisticConflict
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	s.version++
	s.events = append(s.events, OrderEvent{
		ID:        s.nextID,
		BranchID:  1,
		OrderID:   s.events[0].OrderID,
		EventType: eventType,
		Payload:   raw,
		CreatedAt: time.Now().UTC(),
	})
	s.nextID++
	return nil
}

// Events returns a copy of all recorded events.
func (s *OptimisticOrderStore) Events() []OrderEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OrderEvent, len(s.events))
	copy(out, s.events)
	return out
}
