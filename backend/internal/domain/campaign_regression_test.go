package domain_test

import (
	"encoding/json"
	"github.com/efehidir/ddo/backend/internal/domain"
	"testing"
)

func TestCampaignFreeUnitsUseActualVariantPrices(t *testing.T) {
	items := []domain.BasketItem{{MenuItemCode: "X", Quantity: 1, UnitPriceKurus: 100, LineSubtotalKurus: 100}, {MenuItemCode: "X", Quantity: 5, UnitPriceKurus: 200, LineSubtotalKurus: 1000}}
	rules := []domain.CampaignRule{{Code: "FREE", RuleType: "BUY_X_PAY_Y", Stackable: true, IsActive: true, ConfigJSON: json.RawMessage(`{"item_code":"X","buy_qty":3,"pay_qty":2}`)}}
	result := domain.EvaluateBestCampaigns(items, rules, nil, 12)
	if result.TotalDiscountKurus != 300 {
		t.Fatalf("two cheapest real units cost 300: %+v", result)
	}
}

func TestCampaignCapMatchesExplanation(t *testing.T) {
	items := []domain.BasketItem{{MenuItemCode: "X", Quantity: 1, UnitPriceKurus: 100, LineSubtotalKurus: 100}}
	rules := []domain.CampaignRule{{Code: "HALF", RuleType: "HAPPY_HOUR", Priority: 10, Stackable: true, IsActive: true, ConfigJSON: json.RawMessage(`{"start_hour":0,"end_hour":24,"discount_pct":50}`)}}
	coupon := &domain.CouponRule{Code: "C", IsActive: true, Stackable: true, DiscountType: "FIXED", DiscountValue: 100}
	result := domain.EvaluateBestCampaigns(items, rules, coupon, 12)
	var sum int64
	for _, c := range result.AppliedCampaigns {
		sum += c.DiscountKurus
	}
	if sum != result.TotalDiscountKurus || sum != 100 {
		t.Fatalf("explanation drift: %+v", result)
	}
}

func TestCampaignOvernightWindow(t *testing.T) {
	items := []domain.BasketItem{{Quantity: 1, LineSubtotalKurus: 1000}}
	rules := []domain.CampaignRule{{Code: "NIGHT", RuleType: "HAPPY_HOUR", Stackable: true, IsActive: true, ConfigJSON: json.RawMessage(`{"start_hour":22,"end_hour":2,"discount_pct":10}`)}}
	for _, hour := range []int{22, 23, 0, 1} {
		if domain.EvaluateBestCampaigns(items, rules, nil, hour).TotalDiscountKurus != 100 {
			t.Fatalf("night hour %d rejected", hour)
		}
	}
	if domain.EvaluateBestCampaigns(items, rules, nil, 2).TotalDiscountKurus != 0 {
		t.Fatal("exclusive end hour included")
	}
}

func TestDiscountAllocationLargeAmounts(t *testing.T) {
	items := []domain.LineItemInput{{ID: 1, LineSubtotalKurus: 10000000000}, {ID: 2, LineSubtotalKurus: 20000000000}}
	lines, err := domain.AllocateDiscountAcrossItems(items, 10000000000)
	if err != nil || lines[0].AllocatedDiscountKurus+lines[1].AllocatedDiscountKurus != 10000000000 {
		t.Fatalf("overflow or drift: %+v %v", lines, err)
	}
}
