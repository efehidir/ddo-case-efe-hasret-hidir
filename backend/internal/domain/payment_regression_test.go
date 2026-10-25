package domain_test

import (
	"encoding/json"
	"github.com/efehidir/ddo/backend/internal/domain"
	"testing"
)

func TestPaymentAllocationAfterPartialPayment(t *testing.T) {
	items := []domain.AllocatedLineItem{
		{ID: 1, NetTotalKurus: 10001, RemainingKurus: 10001},
		{ID: 2, NetTotalKurus: 10000, RemainingKurus: 10000},
	}
	paid, err := domain.AllocatePayment(items, 5000, nil)
	if err != nil || paid[1] != 5000 || paid[2] != 0 {
		t.Fatalf("partial payment: %v %v", paid, err)
	}
	items[0].PaidKurus, items[0].RemainingKurus = 5000, 5001
	paid, err = domain.AllocatePayment(items, 5001, []int64{1, 1})
	if err != nil || paid[1] != 5001 {
		t.Fatalf("remaining item payment: %v %v", paid, err)
	}
	if _, err := domain.AllocatePayment(items, 1, []int64{2}); err == nil {
		t.Fatal("underpayment marked an item settled")
	}
	if _, err := domain.AllocatePayment(items, 16000, nil); err == nil {
		t.Fatal("overpayment accepted")
	}
	if _, err := domain.AllocatePayment(items, 1, []int64{999}); err == nil {
		t.Fatal("foreign item accepted")
	}
}

func TestMergedSourceRebuild(t *testing.T) {
	item, _ := json.Marshal(domain.ItemAddedPayload{OrderItemID: 1, LineSubtotalKurus: 11000})
	merge, _ := json.Marshal(domain.TableMergedPayload{SourceOrderID: 1, TargetOrderID: 2, TransferredTotalKurus: 11000})
	state, err := domain.RebuildOrderStateFromEvents([]domain.OrderEvent{
		{OrderID: 1, EventType: domain.EventOrderOpened, Payload: json.RawMessage(`{"table_id":1}`)},
		{OrderID: 1, EventType: domain.EventItemAdded, Payload: item},
		{OrderID: 1, EventType: domain.EventTableMerged, Payload: merge},
	})
	if err != nil || state.Status != "MERGED" || state.TotalKurus != 0 || state.ActiveItemCount != 0 {
		t.Fatalf("bad merged source: %+v %v", state, err)
	}
}
