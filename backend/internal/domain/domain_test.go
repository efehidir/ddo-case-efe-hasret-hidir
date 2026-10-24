package domain_test

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/efehidir/ddo/backend/internal/domain"
)

// ============================================================================
// T-1: Adisyon Tutarlılığı (Order Event Log Rebuild & Optimistic Concurrency)
// ============================================================================

func TestT1_RebuildOrderStateFromEvents_EmptyLog(t *testing.T) {
	if _, err := domain.RebuildOrderStateFromEvents(nil); err == nil {
		t.Fatalf("expected error on empty event log")
	}
}

func TestT1_RebuildOrderStateFromEvents_FullLifecycle(t *testing.T) {
	mustJSON := func(v any) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}

	events := []domain.OrderEvent{
		{ID: 1, OrderID: 100, EventType: domain.EventOrderOpened, Payload: mustJSON(map[string]int64{"table_id": 3})},
		{ID: 2, OrderID: 100, EventType: domain.EventItemAdded, Payload: mustJSON(domain.ItemAddedPayload{
			OrderItemID: 1, MenuItemID: 1, ItemName: "İskender", Quantity: 2, UnitPriceKurus: 36000, LineSubtotalKurus: 72000,
		})},
		{ID: 3, OrderID: 100, EventType: domain.EventItemAdded, Payload: mustJSON(domain.ItemAddedPayload{
			OrderItemID: 2, MenuItemID: 2, ItemName: "Lahmacun", Quantity: 1, UnitPriceKurus: 11000, LineSubtotalKurus: 11000,
		})},
		{ID: 4, OrderID: 100, EventType: domain.EventItemCancelled, Payload: mustJSON(domain.ItemCancelledPayload{
			OrderItemID: 2, LineSubtotalKurus: 11000, IsProduced: true, WasteRecorded: true, Reason: "Müşteri vazgeçti",
		})},
		{ID: 5, OrderID: 100, EventType: domain.EventTableMoved, Payload: mustJSON(domain.TableMovedPayload{
			FromTableID: 3, ToTableID: 7,
		})},
		{ID: 6, OrderID: 100, EventType: domain.EventTableMerged, Payload: mustJSON(domain.TableMergedPayload{
			SourceOrderID: 101, TargetOrderID: 100, TransferredTotalKurus: 19000,
		})},
		{ID: 7, OrderID: 100, EventType: domain.EventItemAdded, Payload: mustJSON(domain.ItemAddedPayload{
			OrderItemID: 3, MenuItemID: 5, ItemName: "Künefe (Transfer)", Quantity: 1, UnitPriceKurus: 19000, LineSubtotalKurus: 19000,
		})},
		{ID: 8, OrderID: 100, EventType: domain.EventCampaignApplied, Payload: mustJSON(domain.CampaignAppliedPayload{
			DiscountKurus: 5000, CouponCode: "SOFRA50",
		})},
		{ID: 9, OrderID: 100, EventType: domain.EventPaymentReceived, Payload: mustJSON(domain.PaymentReceivedPayload{
			PaymentID: 1, Method: "CASH", SplitMode: "CUSTOM", AmountKurus: 40000, TipKurus: 2000,
		})},
	}

	state, err := domain.RebuildOrderStateFromEvents(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.CurrentTableID != 7 {
		t.Errorf("expected table 7 after move, got %d", state.CurrentTableID)
	}
	if state.SubtotalKurus != 91000 { // 72000 + 11000 - 11000 + 19000
		t.Errorf("expected subtotal 91000, got %d", state.SubtotalKurus)
	}
	if state.DiscountKurus != 5000 {
		t.Errorf("expected discount 5000, got %d", state.DiscountKurus)
	}
	if state.TotalKurus != 86000 {
		t.Errorf("expected total 86000, got %d", state.TotalKurus)
	}
	if state.PaidKurus != 40000 || state.RemainingKurus != 46000 {
		t.Errorf("expected paid 40000 & remaining 46000, got %d / %d", state.PaidKurus, state.RemainingKurus)
	}
	if state.Status != "PARTIALLY_PAID" {
		t.Errorf("expected PARTIALLY_PAID, got %s", state.Status)
	}
	if state.WasteItemCount != 1 {
		t.Errorf("expected waste item count 1, got %d", state.WasteItemCount)
	}
}

func TestT1_RebuildOrderStateFromEvents_FullyPaid(t *testing.T) {
	b1, _ := json.Marshal(domain.ItemAddedPayload{OrderItemID: 1, LineSubtotalKurus: 20000})
	b2, _ := json.Marshal(domain.PaymentReceivedPayload{AmountKurus: 20000, TipKurus: 1500})
	state, err := domain.RebuildOrderStateFromEvents([]domain.OrderEvent{
		{ID: 1, OrderID: 55, EventType: domain.EventItemAdded, Payload: b1},
		{ID: 2, OrderID: 55, EventType: domain.EventPaymentReceived, Payload: b2},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != "PAID" || state.RemainingKurus != 0 || state.TipKurus != 1500 {
		t.Errorf("unexpected paid state: %+v", state)
	}
}

func TestT1_ConcurrentWaiters_OptimisticLockingRaceProof(t *testing.T) {
	store := domain.NewOptimisticOrderStore(500, 4)

	const concurrentWaiters = 20
	var wg sync.WaitGroup
	var conflictsDetected int64

	wg.Add(concurrentWaiters)
	for w := 0; w < concurrentWaiters; w++ {
		itemID := int64(w + 1)
		go func(id int64) {
			defer wg.Done()
			for {
				ver := store.ReadVersion()
				err := store.AppendEventWithVersion(ver, domain.EventItemAdded, domain.ItemAddedPayload{
					OrderItemID:       id,
					MenuItemID:        1,
					ItemName:          "Lahmacun",
					Quantity:          1,
					UnitPriceKurus:    11000,
					LineSubtotalKurus: 11000,
				})
				if err == nil {
					return
				}
				if err == domain.ErrOptimisticConflict {
					atomic.AddInt64(&conflictsDetected, 1)
					continue
				}
				t.Errorf("unexpected error: %v", err)
				return
			}
		}(itemID)
	}
	wg.Wait()

	state, err := domain.RebuildOrderStateFromEvents(store.Events())
	if err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	if state.ActiveItemCount != concurrentWaiters {
		t.Errorf("expected %d items after concurrent optimistic retry, got %d", concurrentWaiters, state.ActiveItemCount)
	}
	if state.SubtotalKurus != int64(concurrentWaiters)*11000 {
		t.Errorf("expected subtotal %d, got %d", concurrentWaiters*11000, state.SubtotalKurus)
	}
	if store.ReadVersion() != int64(concurrentWaiters+1) {
		t.Errorf("expected version %d, got %d", concurrentWaiters+1, store.ReadVersion())
	}
}

func TestT1_AddItemDuringPayment_ConsistentRemaining(t *testing.T) {
	store := domain.NewOptimisticOrderStore(600, 2)
	_ = store.AppendEventWithVersion(1, domain.EventItemAdded, domain.ItemAddedPayload{
		OrderItemID: 1, LineSubtotalKurus: 36000,
	})
	_ = store.AppendEventWithVersion(2, domain.EventPaymentReceived, domain.PaymentReceivedPayload{
		PaymentID: 1, Method: "CARD", AmountKurus: 20000,
	})
	// Waiter adds another item while partial payment is already on the order
	_ = store.AppendEventWithVersion(3, domain.EventItemAdded, domain.ItemAddedPayload{
		OrderItemID: 2, LineSubtotalKurus: 4500,
	})

	state, err := domain.RebuildOrderStateFromEvents(store.Events())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.TotalKurus != 40500 || state.PaidKurus != 20000 || state.RemainingKurus != 20500 {
		t.Errorf("unexpected totals after adding item during payment: %+v", state)
	}
}

// ============================================================================
// T-2: Hesap Bölüşme, Kuruş Kaymasız İndirim Dağıtımı ve Z Raporu
// ============================================================================

func TestT2_AllocateDiscountAcrossItems_ZeroKurusDrift_PrimeNumbers(t *testing.T) {
	items := []domain.LineItemInput{
		{ID: 1, Name: "Kalem A", Quantity: 1, UnitPriceKurus: 10000, LineSubtotalKurus: 10000},
		{ID: 2, Name: "Kalem B", Quantity: 1, UnitPriceKurus: 10000, LineSubtotalKurus: 10000},
		{ID: 3, Name: "Kalem C", Quantity: 1, UnitPriceKurus: 10000, LineSubtotalKurus: 10000},
	}

	// 1000 kurus (10.00 TL) discount across 3 equal items -> 334 + 333 + 333 = 1000 kurus!
	allocated, err := domain.AllocateDiscountAcrossItems(items, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var discSum, netSum int64
	for _, a := range allocated {
		discSum += a.AllocatedDiscountKurus
		netSum += a.NetTotalKurus
	}

	if discSum != 1000 {
		t.Errorf("expected exact discount sum 1000, got %d", discSum)
	}
	if netSum != 29000 {
		t.Errorf("expected exact net sum 29000, got %d", netSum)
	}
	if allocated[0].AllocatedDiscountKurus != 334 || allocated[1].AllocatedDiscountKurus != 333 || allocated[2].AllocatedDiscountKurus != 333 {
		t.Errorf("unexpected individual allocations: %+v", allocated)
	}
}

func TestT2_AllocateDiscountAcrossItems_WithCancelledItem(t *testing.T) {
	items := []domain.LineItemInput{
		{ID: 1, Name: "İskender", Quantity: 1, UnitPriceKurus: 36000, LineSubtotalKurus: 36000},
		{ID: 2, Name: "İptal Lahmacun", Quantity: 1, UnitPriceKurus: 11000, LineSubtotalKurus: 11000, IsCancelled: true},
		{ID: 3, Name: "Künefe", Quantity: 1, UnitPriceKurus: 19000, LineSubtotalKurus: 19000},
	}

	allocated, err := domain.AllocateDiscountAcrossItems(items, 5555)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if allocated[1].AllocatedDiscountKurus != 0 || allocated[1].NetTotalKurus != 0 {
		t.Errorf("cancelled item must receive 0 discount and 0 net total")
	}
	if allocated[0].AllocatedDiscountKurus+allocated[2].AllocatedDiscountKurus != 5555 {
		t.Errorf("active items discount sum must equal 5555")
	}
}

func TestT2_AllocateDiscountAcrossItems_NegativeError(t *testing.T) {
	if _, err := domain.AllocateDiscountAcrossItems(nil, -10); err == nil {
		t.Errorf("expected error for negative discount")
	}
}

func TestT2_CalculateEqualSplit_ExactKurusConservation(t *testing.T) {
	// 100.00 TL (10000 kurus) split 3 ways -> [3334, 3333, 3333]
	shares, err := domain.CalculateEqualSplit(10000, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(shares) != 3 || shares[0] != 3334 || shares[1] != 3333 || shares[2] != 3333 {
		t.Errorf("unexpected shares: %v", shares)
	}
	if shares[0]+shares[1]+shares[2] != 10000 {
		t.Errorf("sum of equal split shares must equal 10000")
	}
}

func TestT2_CalculateEqualSplit_InvalidInputs(t *testing.T) {
	if _, err := domain.CalculateEqualSplit(10000, 0); err == nil {
		t.Errorf("expected error for parts=0")
	}
	if _, err := domain.CalculateEqualSplit(0, 2); err == nil {
		t.Errorf("expected error for remaining=0")
	}
}

func TestT2_CalculateItemSplit(t *testing.T) {
	items := []domain.AllocatedLineItem{
		{ID: 10, NetTotalKurus: 32000, PaidKurus: 0, RemainingKurus: 32000},
		{ID: 11, NetTotalKurus: 9500, PaidKurus: 0, RemainingKurus: 9500},
		{ID: 12, NetTotalKurus: 4500, PaidKurus: 4500, RemainingKurus: 0},
	}

	amt, err := domain.CalculateItemSplit(items, []int64{10, 11})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amt != 41500 {
		t.Errorf("expected 41500, got %d", amt)
	}

	if _, err := domain.CalculateItemSplit(items, []int64{12}); err == nil {
		t.Errorf("expected error when selecting already paid item")
	}
	if _, err := domain.CalculateItemSplit(items, []int64{999}); err == nil {
		t.Errorf("expected error for unknown item ID")
	}
}

func TestT2_ReconcileZReport_ExactMatch(t *testing.T) {
	payments := []domain.PaymentRecord{
		{ID: 1, OrderID: 1, Method: "CASH", SplitMode: "EQUAL", AmountKurus: 3334, TipKurus: 500},
		{ID: 2, OrderID: 1, Method: "CARD", SplitMode: "EQUAL", AmountKurus: 3333, TipKurus: 1000},
		{ID: 3, OrderID: 1, Method: "CARD", SplitMode: "EQUAL", AmountKurus: 3333, TipKurus: 0},
	}

	report := domain.ReconcileZReport(payments, 10000)
	if !report.IsBalanced {
		t.Errorf("expected Z-Report to be balanced")
	}
	if report.CashPaymentsKurus != 3334 || report.CardPaymentsKurus != 6666 {
		t.Errorf("unexpected method breakdown: %+v", report)
	}
	if report.TotalPaymentsKurus != 10000 || report.TotalTipsKurus != 1500 || report.GrandTotalKurus != 11500 {
		t.Errorf("unexpected totals: %+v", report)
	}
}

// ============================================================================
// T-3: Gerçek Zamanlı Mutfak Ekranı (KDS) & SSE Replay
// ============================================================================

func TestT3_EnrichAndSortKDSItems_CourseAndDelayOrdering(t *testing.T) {
	now := time.Now().UTC()
	items := []domain.KDSTicketItem{
		{
			OrderItemID: 1, ItemName: "Künefe", Station: "bar", Course: 3, PrepTimeSec: 360,
			KDSStatus: "PENDING", CreatedAt: now.Add(-60 * time.Second),
		},
		{
			OrderItemID: 2, ItemName: "Haydari", Station: "cold", Course: 1, PrepTimeSec: 120,
			KDSStatus: "PENDING", CreatedAt: now.Add(-50 * time.Second),
		},
		{
			OrderItemID: 3, ItemName: "Geciken İskender", Station: "hot", Course: 2, PrepTimeSec: 300,
			KDSStatus: "PREPARING", CreatedAt: now.Add(-450 * time.Second), // 450s > 300s -> DELAYED!
		},
	}

	sorted := domain.EnrichAndSortKDSItems(items, now)
	if len(sorted) != 3 {
		t.Fatalf("expected 3 items, got %d", len(sorted))
	}
	// Delayed item must bubble to top first
	if sorted[0].OrderItemID != 3 || !sorted[0].IsDelayed {
		t.Errorf("expected delayed item #3 first, got %+v", sorted[0])
	}
	// Next should be Course 1 (Haydari), then Course 3 (Künefe)
	if sorted[1].OrderItemID != 2 || sorted[2].OrderItemID != 1 {
		t.Errorf("expected course order [3, 2, 1], got [%d, %d, %d]",
			sorted[0].OrderItemID, sorted[1].OrderItemID, sorted[2].OrderItemID)
	}
}

func TestT3_KDSBroker_PubSubAndDisconnectReplay(t *testing.T) {
	broker := domain.NewKDSBroker(100)
	ch, unsub := broker.Subscribe()

	broker.Publish(domain.KDSStreamEvent{
		EventID: 101, BranchID: 1, OrderID: 10, Station: "hot", EventType: "ITEM_ADDED", Summary: "İskender x2",
	})

	select {
	case ev := <-ch:
		if ev.EventID != 101 {
			t.Errorf("expected event 101, got %d", ev.EventID)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for live SSE event")
	}

	// Simulate disconnect: unsubscribe client while events 102 and 103 happen
	unsub()

	broker.Publish(domain.KDSStreamEvent{
		EventID: 102, BranchID: 1, OrderID: 10, Station: "hot", EventType: "ITEM_KDS_STATUS_CHANGED", Summary: "İskender -> PREPARING",
	})
	broker.Publish(domain.KDSStreamEvent{
		EventID: 103, BranchID: 1, OrderID: 10, Station: "cold", EventType: "ITEM_ADDED", Summary: "Haydari x1",
	})

	// Client reconnects with Last-Event-ID = 101
	missedAll := broker.ReplaySince(1, 101, "all")
	if len(missedAll) != 2 || missedAll[0].EventID != 102 || missedAll[1].EventID != 103 {
		t.Errorf("expected missed events [102, 103], got %+v", missedAll)
	}

	// Station-filtered replay ("hot" only)
	missedHot := broker.ReplaySince(1, 101, "hot")
	if len(missedHot) != 1 || missedHot[0].EventID != 102 {
		t.Errorf("expected 1 hot missed event [102], got %+v", missedHot)
	}
}

// ============================================================================
// T-4: Reçete Bazlı Stok, Alt Reçete (Yarı Mamul) ve Gün Sonu Sayım Farkı
// ============================================================================

func TestT4_ResolveRecipeTree_WithSubRecipeVariantAndModifiers(t *testing.T) {
	edges := []domain.RecipeEdge{
		// Sub-recipe SEMI-ISKENDER-SAUCE (150g = 120g tomato + 30g butter)
		{MenuItemID: 0, SemiFinishedCode: "SEMI-ISKENDER-SAUCE", IngredientCode: "ING-TOMATO", QtyGrams: 120},
		{MenuItemID: 0, SemiFinishedCode: "SEMI-ISKENDER-SAUCE", IngredientCode: "ING-BUTTER", QtyGrams: 30},
		// Product 1 (İskender): 180g beef + 150g SEMI-ISKENDER-SAUCE + 80g yogurt
		{MenuItemID: 1, IngredientCode: "ING-BEEF", QtyGrams: 180},
		{MenuItemID: 1, IngredientCode: "SEMI-ISKENDER-SAUCE", QtyGrams: 150},
		{MenuItemID: 1, IngredientCode: "ING-YOGURT", QtyGrams: 80},
	}

	// Order: 2 quantities of "1.5 Porsiyon" (150% multiplier) + "Ekstra Tereyağı" (+30g per item)
	reqs, err := domain.ResolveRecipeTree(
		1,
		2,
		150,
		[]domain.ModifierRecipeEffect{
			{Name: "Ekstra Tereyağı", IngredientCode: "ING-BUTTER", ExtraQtyGrams: 30},
		},
		edges,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := make(map[string]int64)
	for _, r := range reqs {
		got[r.IngredientCode] = r.TotalGrams
	}

	// Beef: 180g * 1.5 * 2 = 540g
	if got["ING-BEEF"] != 540 {
		t.Errorf("expected ING-BEEF 540g, got %d", got["ING-BEEF"])
	}
	// Tomato (from sub-recipe): 120g * 1.5 * 2 = 360g
	if got["ING-TOMATO"] != 360 {
		t.Errorf("expected ING-TOMATO 360g from sub-recipe, got %d", got["ING-TOMATO"])
	}
	// Butter: (30g from sub-recipe * 1.5 * 2 = 90g) + (30g modifier * 2 = 60g) = 150g
	if got["ING-BUTTER"] != 150 {
		t.Errorf("expected ING-BUTTER 150g, got %d", got["ING-BUTTER"])
	}
	// Yogurt: 80g * 1.5 * 2 = 240g
	if got["ING-YOGURT"] != 240 {
		t.Errorf("expected ING-YOGURT 240g, got %d", got["ING-YOGURT"])
	}
}

func TestT4_ComputeStockVarianceReport(t *testing.T) {
	inputs := []domain.StockCountInput{
		{
			IngredientID: 1, IngredientCode: "ING-BEEF", IngredientName: "Dana Döner",
			Unit: "g", TheoreticalGrams: 10000, CountedGrams: 9600, CostPerUnitKurus: 65,
		},
		{
			IngredientID: 2, IngredientCode: "ING-TOMATO", IngredientName: "Domates",
			Unit: "g", TheoreticalGrams: 5000, CountedGrams: 5000, CostPerUnitKurus: 8,
		},
	}

	report := domain.ComputeStockVarianceReport(inputs)
	if report.TotalDeficitGrams != 400 {
		t.Errorf("expected total deficit 400g, got %d", report.TotalDeficitGrams)
	}
	if report.TotalDeficitCostKurus != 400*65 {
		t.Errorf("expected deficit cost %d, got %d", 400*65, report.TotalDeficitCostKurus)
	}
	if report.Rows[0].Status != "DEFICIT_WASTE" || report.Rows[1].Status != "MATCH" {
		t.Errorf("unexpected row statuses: %+v", report.Rows)
	}
}

// ============================================================================
// T-5: Kampanya Motoru (En Avantajlı Kombinasyon ve Açıklanabilirlik)
// ============================================================================

func TestT5_EvaluateBestCampaigns_StackableWinsOverExclusive(t *testing.T) {
	campaigns := []domain.CampaignRule{
		{
			Code: "CMP-3AL2ODE", Name: "3 Al 2 Öde Lahmacun", RuleType: "BUY_X_PAY_Y",
			Priority: 20, Stackable: true, IsActive: true,
			ConfigJSON: json.RawMessage(`{"item_code":"MN-LAHMACUN","buy_qty":3,"pay_qty":2}`),
		},
		{
			Code: "CMP-COMBO-KEBAP", Name: "İskender + Ayran + Künefe Kombo", RuleType: "COMBO",
			Priority: 30, Stackable: true, IsActive: true,
			ConfigJSON: json.RawMessage(`{"required_codes":["MN-ISKENDER","MN-AYRAN","MN-KUNEFE"],"discount_kurus":8500}`),
		},
		{
			Code: "CMP-HAPPY-HOUR", Name: "%10 Saatlik İndirim", RuleType: "HAPPY_HOUR",
			Priority: 10, Stackable: false, IsActive: true,
			ConfigJSON: json.RawMessage(`{"discount_pct":10,"start_hour":0,"end_hour":24}`),
		},
	}

	basket := []domain.BasketItem{
		{MenuItemCode: "MN-LAHMACUN", ItemName: "Lahmacun", Quantity: 3, UnitPriceKurus: 11000, LineSubtotalKurus: 33000},
		{MenuItemCode: "MN-ISKENDER", ItemName: "İskender", Quantity: 1, UnitPriceKurus: 36000, LineSubtotalKurus: 36000},
		{MenuItemCode: "MN-AYRAN", ItemName: "Ayran", Quantity: 1, UnitPriceKurus: 4500, LineSubtotalKurus: 4500},
		{MenuItemCode: "MN-KUNEFE", ItemName: "Künefe", Quantity: 1, UnitPriceKurus: 19000, LineSubtotalKurus: 19000},
	}
	// Subtotal = 33000 + 36000 + 4500 + 19000 = 92500 kurus (925.00 TL)
	// Stackable bundle: 11000 (3Al2Öde) + 8500 (Combo) + 5000 (SOFRA50) = 24500 kurus
	// Exclusive Happy Hour (%10): 9250 kurus
	coupon := &domain.CouponRule{
		Code: "SOFRA50", DiscountType: "FIXED", DiscountValue: 5000,
		MinOrderKurus: 30000, MaxDiscountKurus: 5000, Stackable: true, IsActive: true,
	}

	res := domain.EvaluateBestCampaigns(basket, campaigns, coupon, 19)
	if res.SubtotalKurus != 92500 {
		t.Errorf("expected subtotal 92500, got %d", res.SubtotalKurus)
	}
	if res.TotalDiscountKurus != 24500 {
		t.Errorf("expected best discount 24500, got %d", res.TotalDiscountKurus)
	}
	if len(res.AppliedCampaigns) != 3 {
		t.Errorf("expected 3 stackable campaigns/coupons applied, got %d", len(res.AppliedCampaigns))
	}
}

func TestT5_EvaluateBestCampaigns_ExclusiveVIPWinsWhenBetter(t *testing.T) {
	campaigns := []domain.CampaignRule{
		{
			Code: "CMP-HAPPY-HOUR", Name: "%10 Saatlik İndirim", RuleType: "HAPPY_HOUR",
			Priority: 10, Stackable: false, IsActive: true,
			ConfigJSON: json.RawMessage(`{"discount_pct":10,"start_hour":0,"end_hour":24}`),
		},
	}

	basket := []domain.BasketItem{
		{MenuItemCode: "MN-ISKENDER", ItemName: "İskender", Quantity: 2, UnitPriceKurus: 36000, LineSubtotalKurus: 72000},
	}
	// Subtotal = 72000. VIP20 (%20 non-stackable) = 14400 kurus vs Happy Hour (%10) = 7200 kurus.
	coupon := &domain.CouponRule{
		Code: "VIP20", DiscountType: "PERCENT", DiscountValue: 20,
		MinOrderKurus: 50000, MaxDiscountKurus: 25000, Stackable: false, IsActive: true,
	}

	res := domain.EvaluateBestCampaigns(basket, campaigns, coupon, 19)
	if res.TotalDiscountKurus != 14400 {
		t.Errorf("expected VIP20 discount 14400 to win, got %d", res.TotalDiscountKurus)
	}
	if len(res.AppliedCampaigns) != 1 || res.AppliedCampaigns[0].Code != "VIP20" {
		t.Errorf("expected VIP20 as sole applied campaign, got %+v", res.AppliedCampaigns)
	}
}

func TestT5_EvaluateBestCampaigns_CouponBelowMinimum(t *testing.T) {
	basket := []domain.BasketItem{
		{MenuItemCode: "MN-AYRAN", ItemName: "Ayran", Quantity: 2, UnitPriceKurus: 4500, LineSubtotalKurus: 9000},
	}
	coupon := &domain.CouponRule{
		Code: "SOFRA50", DiscountType: "FIXED", DiscountValue: 5000,
		MinOrderKurus: 30000, MaxDiscountKurus: 5000, Stackable: true, IsActive: true,
	}
	res := domain.EvaluateBestCampaigns(basket, nil, coupon, 14)
	if res.TotalDiscountKurus != 0 {
		t.Errorf("expected 0 discount when below min order, got %d", res.TotalDiscountKurus)
	}
	if len(res.AllEvaluations) != 1 || res.AllEvaluations[0].Applied {
		t.Errorf("expected rejected coupon evaluation with explanation")
	}
}

func TestT5_EvaluateBestCampaigns_InactiveCouponAndEmptyBasket(t *testing.T) {
	resEmpty := domain.EvaluateBestCampaigns(nil, nil, nil, 12)
	if resEmpty.SubtotalKurus != 0 {
		t.Errorf("expected 0 for empty basket")
	}

	basket := []domain.BasketItem{
		{MenuItemCode: "MN-ISKENDER", ItemName: "İskender", Quantity: 1, UnitPriceKurus: 36000, LineSubtotalKurus: 36000},
	}
	inactiveCoupon := &domain.CouponRule{
		Code: "OLD10", IsActive: false,
	}
	res := domain.EvaluateBestCampaigns(basket, nil, inactiveCoupon, 12)
	if res.TotalDiscountKurus != 0 || res.WinningStrategy != "NO_DISCOUNT" {
		t.Errorf("expected NO_DISCOUNT for inactive coupon")
	}
}

func TestT5_EvaluateBestCampaigns_HappyHourOutsideWindow(t *testing.T) {
	campaigns := []domain.CampaignRule{
		{
			Code: "CMP-HAPPY-HOUR", Name: "Öğle İndirimi", RuleType: "HAPPY_HOUR",
			Priority: 10, Stackable: false, IsActive: true,
			ConfigJSON: json.RawMessage(`{"discount_pct":15,"start_hour":12,"end_hour":15}`),
		},
	}
	basket := []domain.BasketItem{
		{MenuItemCode: "MN-ISKENDER", ItemName: "İskender", Quantity: 1, UnitPriceKurus: 36000, LineSubtotalKurus: 36000},
	}
	res := domain.EvaluateBestCampaigns(basket, campaigns, nil, 20)
	if res.TotalDiscountKurus != 0 {
		t.Errorf("expected 0 discount outside happy hour window")
	}
}

func TestT4_ResolveRecipeTree_InvalidQuantity(t *testing.T) {
	if _, err := domain.ResolveRecipeTree(1, 0, 100, nil, nil); err == nil {
		t.Errorf("expected error for quantity 0")
	}
}

func TestT4_ResolveRecipeTree_DefaultMultiplier(t *testing.T) {
	edges := []domain.RecipeEdge{
		{MenuItemID: 4, IngredientCode: "ING-YOGURT", QtyGrams: 120},
	}
	reqs, err := domain.ResolveRecipeTree(4, 1, 0, nil, edges)
	if err != nil || len(reqs) != 1 || reqs[0].TotalGrams != 120 {
		t.Errorf("expected 120g with default multiplier, got %+v (err=%v)", reqs, err)
	}
}

func TestT4_ComputeStockVarianceReport_Surplus(t *testing.T) {
	rep := domain.ComputeStockVarianceReport([]domain.StockCountInput{
		{IngredientID: 1, IngredientCode: "ING-DOUGH", TheoreticalGrams: 1000, CountedGrams: 1150, CostPerUnitKurus: 10},
	})
	if rep.TotalSurplusGrams != 150 || rep.Rows[0].Status != "SURPLUS" {
		t.Errorf("expected SURPLUS of 150g, got %+v", rep)
	}
}

func TestT3_KDSBroker_RingBufferEviction(t *testing.T) {
	broker := domain.NewKDSBroker(3)
	for i := int64(1); i <= 5; i++ {
		broker.Publish(domain.KDSStreamEvent{EventID: i, BranchID: 1, Station: "hot"})
	}
	replayed := broker.ReplaySince(1, 0, "all")
	if len(replayed) != 3 || replayed[0].EventID != 3 || replayed[2].EventID != 5 {
		t.Errorf("expected ring buffer to retain events [3,4,5], got %+v", replayed)
	}
}

func TestT2_AllocateDiscountAcrossItems_CappedAtSubtotal(t *testing.T) {
	items := []domain.LineItemInput{
		{ID: 1, Name: "Ayran", Quantity: 1, UnitPriceKurus: 4500, LineSubtotalKurus: 4500},
	}
	allocated, err := domain.AllocateDiscountAcrossItems(items, 99000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allocated[0].AllocatedDiscountKurus != 4500 || allocated[0].NetTotalKurus != 0 {
		t.Errorf("expected discount capped at 4500, got %+v", allocated[0])
	}
}

func TestT2_CalculateItemSplit_EmptySelection(t *testing.T) {
	if _, err := domain.CalculateItemSplit(nil, nil); err == nil {
		t.Errorf("expected error on empty item selection")
	}
}

func TestT2_CalculateItemSplit_DuplicateIDsIgnored(t *testing.T) {
	items := []domain.AllocatedLineItem{
		{ID: 5, NetTotalKurus: 11000, RemainingKurus: 11000},
	}
	sum, err := domain.CalculateItemSplit(items, []int64{5, 5, 5})
	if err != nil || sum != 11000 {
		t.Errorf("expected 11000 when duplicate IDs passed, got %d (err=%v)", sum, err)
	}
}
