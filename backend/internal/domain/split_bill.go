package domain

import (
	"errors"
	"sort"
)

// LineItemInput represents an active order line item before discount allocation.
type LineItemInput struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Quantity          int    `json:"quantity"`
	UnitPriceKurus    int64  `json:"unit_price_kurus"`
	LineSubtotalKurus int64  `json:"line_subtotal_kurus"`
	PaidKurus         int64  `json:"paid_kurus"`
	IsCancelled       bool   `json:"is_cancelled"`
}

// AllocatedLineItem holds the exact integer kurus discount and net total for an item.
type AllocatedLineItem struct {
	ID                     int64  `json:"id"`
	Name                   string `json:"name"`
	Quantity               int    `json:"quantity"`
	UnitPriceKurus         int64  `json:"unit_price_kurus"`
	LineSubtotalKurus      int64  `json:"line_subtotal_kurus"`
	AllocatedDiscountKurus int64  `json:"allocated_discount_kurus"`
	NetTotalKurus          int64  `json:"net_total_kurus"`
	PaidKurus              int64  `json:"paid_kurus"`
	RemainingKurus         int64  `json:"remaining_kurus"`
}

// AllocateDiscountAcrossItems distributes totalDiscountKurus proportionally across active items
// using integer largest-remainder arithmetic so that sum(AllocatedDiscountKurus) == totalDiscountKurus
// with zero kurus drift.
func AllocateDiscountAcrossItems(items []LineItemInput, totalDiscountKurus int64) ([]AllocatedLineItem, error) {
	if totalDiscountKurus < 0 {
		return nil, errors.New("total discount cannot be negative")
	}

	var subtotal int64
	activeIndices := make([]int, 0, len(items))
	for i, it := range items {
		if !it.IsCancelled && it.LineSubtotalKurus > 0 {
			subtotal += it.LineSubtotalKurus
			activeIndices = append(activeIndices, i)
		}
	}

	if totalDiscountKurus > subtotal {
		totalDiscountKurus = subtotal
	}

	result := make([]AllocatedLineItem, len(items))
	for i, it := range items {
		result[i] = AllocatedLineItem{
			ID:                it.ID,
			Name:              it.Name,
			Quantity:          it.Quantity,
			UnitPriceKurus:    it.UnitPriceKurus,
			LineSubtotalKurus: it.LineSubtotalKurus,
			NetTotalKurus:     it.LineSubtotalKurus,
			PaidKurus:         it.PaidKurus,
			RemainingKurus:    maxInt64(0, it.LineSubtotalKurus-it.PaidKurus),
		}
		if it.IsCancelled {
			result[i].LineSubtotalKurus = 0
			result[i].NetTotalKurus = 0
			result[i].RemainingKurus = 0
		}
	}

	if subtotal == 0 || totalDiscountKurus == 0 || len(activeIndices) == 0 {
		return result, nil
	}

	type remainderSlot struct {
		idx       int
		remainder int64
		itemID    int64
	}

	var allocatedSum int64
	slots := make([]remainderSlot, 0, len(activeIndices))

	for _, idx := range activeIndices {
		sub := items[idx].LineSubtotalKurus
		numerator := sub * totalDiscountKurus
		baseDiscount := numerator / subtotal
		rem := numerator % subtotal

		result[idx].AllocatedDiscountKurus = baseDiscount
		allocatedSum += baseDiscount
		slots = append(slots, remainderSlot{
			idx:       idx,
			remainder: rem,
			itemID:    items[idx].ID,
		})
	}

	leftoverKurus := totalDiscountKurus - allocatedSum
	if leftoverKurus > 0 {
		sort.SliceStable(slots, func(i, j int) bool {
			if slots[i].remainder != slots[j].remainder {
				return slots[i].remainder > slots[j].remainder
			}
			return slots[i].itemID < slots[j].itemID
		})

		for k := int64(0); k < leftoverKurus && int(k) < len(slots); k++ {
			targetIdx := slots[k].idx
			result[targetIdx].AllocatedDiscountKurus++
		}
	}

	for _, idx := range activeIndices {
		result[idx].NetTotalKurus = result[idx].LineSubtotalKurus - result[idx].AllocatedDiscountKurus
		result[idx].RemainingKurus = maxInt64(0, result[idx].NetTotalKurus-result[idx].PaidKurus)
	}

	return result, nil
}

// CalculateEqualSplit divides remainingKurus into `parts` integer shares where the first
// (remainingKurus % parts) shares receive 1 extra kurus so that sum(shares) == remainingKurus.
func CalculateEqualSplit(remainingKurus int64, parts int) ([]int64, error) {
	if parts <= 0 || parts > 100 {
		return nil, errors.New("parts must be greater than zero")
	}
	if remainingKurus <= 0 {
		return nil, errors.New("remaining amount must be positive")
	}

	baseShare := remainingKurus / int64(parts)
	remainder := remainingKurus % int64(parts)

	shares := make([]int64, parts)
	for i := 0; i < parts; i++ {
		shares[i] = baseShare
		if int64(i) < remainder {
			shares[i]++
		}
	}
	return shares, nil
}

// AllocatePayment assigns every paid kuruş to an active line, including partial,
// equal and mixed payments. BY_ITEM must settle exactly the selected lines.
func AllocatePayment(items []AllocatedLineItem, amount int64, selected []int64) (map[int64]int64, error) {
	if amount <= 0 {
		return nil, errors.New("payment must be positive")
	}
	if len(selected) > 0 {
		due, err := CalculateItemSplit(items, selected)
		if err != nil {
			return nil, err
		}
		if due != amount {
			return nil, errors.New("payment must equal the selected items' remaining amount")
		}
	}
	selectedSet := map[int64]bool{}
	for _, id := range selected {
		selectedSet[id] = true
	}
	result := map[int64]int64{}
	remaining := amount
	for _, it := range items {
		if len(selectedSet) > 0 && !selectedSet[it.ID] {
			continue
		}
		part := min(remaining, it.RemainingKurus)
		if part > 0 {
			result[it.ID] = part
			remaining -= part
		}
	}
	if remaining != 0 {
		return nil, errors.New("payment exceeds item balances")
	}
	return result, nil
}

// CalculateItemSplit calculates the exact remaining kurus due for a subset of order item IDs.
func CalculateItemSplit(items []AllocatedLineItem, selectedItemIDs []int64) (int64, error) {
	if len(selectedItemIDs) == 0 {
		return 0, errors.New("at least one item must be selected for BY_ITEM split")
	}

	lookup := make(map[int64]AllocatedLineItem, len(items))
	for _, it := range items {
		lookup[it.ID] = it
	}

	var sum int64
	seen := make(map[int64]bool, len(selectedItemIDs))
	for _, id := range selectedItemIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		it, exists := lookup[id]
		if !exists {
			return 0, errors.New("selected item not found in order")
		}
		if it.RemainingKurus <= 0 {
			return 0, errors.New("selected item is already paid or cancelled")
		}
		sum += it.RemainingKurus
	}

	return sum, nil
}

// PaymentRecord represents a completed payment row for Z-Report reconciliation.
type PaymentRecord struct {
	ID          int64  `json:"id"`
	OrderID     int64  `json:"order_id"`
	Method      string `json:"method"` // "CASH" or "CARD"
	SplitMode   string `json:"split_mode"`
	AmountKurus int64  `json:"amount_kurus"`
	TipKurus    int64  `json:"tip_kurus"`
}

// ZReportSummary holds the reconciled financial totals for a shift/day.
type ZReportSummary struct {
	CashPaymentsKurus  int64 `json:"cash_payments_kurus"`
	CardPaymentsKurus  int64 `json:"card_payments_kurus"`
	TotalPaymentsKurus int64 `json:"total_payments_kurus"`
	TotalTipsKurus     int64 `json:"total_tips_kurus"`
	GrandTotalKurus    int64 `json:"grand_total_kurus"`
	PaymentCount       int   `json:"payment_count"`
	IsBalanced         bool  `json:"is_balanced"`
}

// ReconcileZReport computes payment method totals and verifies exact kurus equality.
func ReconcileZReport(payments []PaymentRecord, closedOrdersNetTotalKurus int64) ZReportSummary {
	var summary ZReportSummary
	summary.PaymentCount = len(payments)

	for _, p := range payments {
		switch p.Method {
		case "CASH":
			summary.CashPaymentsKurus += p.AmountKurus
		case "CARD":
			summary.CardPaymentsKurus += p.AmountKurus
		}
		summary.TotalPaymentsKurus += p.AmountKurus
		summary.TotalTipsKurus += p.TipKurus
	}

	summary.GrandTotalKurus = summary.TotalPaymentsKurus + summary.TotalTipsKurus
	summary.IsBalanced = (summary.CashPaymentsKurus+summary.CardPaymentsKurus == summary.TotalPaymentsKurus) &&
		(closedOrdersNetTotalKurus == 0 || summary.TotalPaymentsKurus == closedOrdersNetTotalKurus)

	return summary
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
