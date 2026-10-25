package domain

import (
	"encoding/json"
	"fmt"
	"sort"
)

// BasketItem represents an order item evaluated by the campaign engine.
type BasketItem struct {
	MenuItemCode      string `json:"menu_item_code"`
	ItemName          string `json:"item_name"`
	Quantity          int    `json:"quantity"`
	UnitPriceKurus    int64  `json:"unit_price_kurus"`
	LineSubtotalKurus int64  `json:"line_subtotal_kurus"`
}

// CampaignRule represents a campaign row from "dz-campaigns".
type CampaignRule struct {
	Code       string          `json:"code"`
	Name       string          `json:"name"`
	RuleType   string          `json:"rule_type"` // "BUY_X_PAY_Y", "COMBO", "HAPPY_HOUR"
	Priority   int             `json:"priority"`
	Stackable  bool            `json:"stackable"`
	ConfigJSON json.RawMessage `json:"config_json"`
	IsActive   bool            `json:"is_active"`
}

// CouponRule represents a coupon row from "dz-coupons".
type CouponRule struct {
	Code             string `json:"code"`
	Description      string `json:"description"`
	DiscountType     string `json:"discount_type"` // "FIXED", "PERCENT"
	DiscountValue    int64  `json:"discount_value"`
	MinOrderKurus    int64  `json:"min_order_kurus"`
	MaxDiscountKurus int64  `json:"max_discount_kurus"`
	Stackable        bool   `json:"stackable"`
	IsActive         bool   `json:"is_active"`
}

// CampaignDecision explains why a specific campaign or coupon was applied or skipped.
type CampaignDecision struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	RuleType      string `json:"rule_type"`
	Applied       bool   `json:"applied"`
	DiscountKurus int64  `json:"discount_kurus"`
	Reason        string `json:"reason"`
}

// CampaignEvaluationResult holds the winning combination, total discount in kurus, and explanations.
type CampaignEvaluationResult struct {
	SubtotalKurus      int64              `json:"subtotal_kurus"`
	TotalDiscountKurus int64              `json:"total_discount_kurus"`
	NetTotalKurus      int64              `json:"net_total_kurus"`
	WinningStrategy    string             `json:"winning_strategy"`
	AppliedCampaigns   []CampaignDecision `json:"applied_campaigns"`
	AllEvaluations     []CampaignDecision `json:"all_evaluations"`
}

// EvaluateBestCampaigns evaluates all active campaigns and optional coupon against the basket,
// respects priority and stackable rules, and chooses the combination that maximizes customer discount.
func EvaluateBestCampaigns(
	items []BasketItem,
	campaigns []CampaignRule,
	coupon *CouponRule,
	currentHour int,
) CampaignEvaluationResult {
	var subtotal int64
	itemQtyByCode := make(map[string]int)
	unitPriceByCode := make(map[string]int64)
	priceTiers := make(map[string][]BasketItem)

	for _, it := range items {
		if it.Quantity <= 0 {
			continue
		}
		subtotal += it.LineSubtotalKurus
		itemQtyByCode[it.MenuItemCode] += it.Quantity
		priceTiers[it.MenuItemCode] = append(priceTiers[it.MenuItemCode], it)
		if unitPriceByCode[it.MenuItemCode] == 0 || it.UnitPriceKurus < unitPriceByCode[it.MenuItemCode] {
			unitPriceByCode[it.MenuItemCode] = it.UnitPriceKurus
		}
	}

	if subtotal == 0 {
		return CampaignEvaluationResult{}
	}

	// Sort campaigns by priority descending
	sortedCampaigns := make([]CampaignRule, len(campaigns))
	copy(sortedCampaigns, campaigns)
	sort.SliceStable(sortedCampaigns, func(i, j int) bool {
		if sortedCampaigns[i].Priority == sortedCampaigns[j].Priority {
			return sortedCampaigns[i].Code < sortedCampaigns[j].Code
		}
		return sortedCampaigns[i].Priority > sortedCampaigns[j].Priority
	})

	type candidateEval struct {
		code      string
		name      string
		ruleType  string
		stackable bool
		eligible  bool
		discount  int64
		reason    string
	}

	candidates := make([]candidateEval, 0, len(sortedCampaigns)+1)

	for _, c := range sortedCampaigns {
		if !c.IsActive {
			continue
		}
		cand := candidateEval{
			code:      c.Code,
			name:      c.Name,
			ruleType:  c.RuleType,
			stackable: c.Stackable,
		}

		switch c.RuleType {
		case "BUY_X_PAY_Y":
			var cfg struct {
				ItemCode string `json:"item_code"`
				BuyQty   int    `json:"buy_qty"`
				PayQty   int    `json:"pay_qty"`
			}
			_ = json.Unmarshal(c.ConfigJSON, &cfg)
			qty := itemQtyByCode[cfg.ItemCode]
			freePerGroup := cfg.BuyQty - cfg.PayQty
			if cfg.BuyQty > 0 && cfg.PayQty >= 0 && freePerGroup > 0 && qty >= cfg.BuyQty {
				groups := qty / cfg.BuyQty
				freeCount := groups * freePerGroup
				tiers := append([]BasketItem(nil), priceTiers[cfg.ItemCode]...)
				sort.SliceStable(tiers, func(i, j int) bool { return tiers[i].UnitPriceKurus < tiers[j].UnitPriceKurus })
				var disc int64
				left := freeCount
				for _, tier := range tiers {
					n := min(left, tier.Quantity)
					disc += int64(n) * tier.UnitPriceKurus
					left -= n
					if left == 0 {
						break
					}
				}
				cand.eligible = true
				cand.discount = disc
				cand.reason = fmt.Sprintf("%s ürününden %d adet alındı; '%d Al %d Öde' kuralı ile %d adet (%s TL) bedava uygulandı.",
					cfg.ItemCode, qty, cfg.BuyQty, cfg.PayQty, freeCount, formatKurus(disc))
			} else {
				cand.reason = fmt.Sprintf("Sepette en az %d adet %s bulunmadığı için koşul sağlanmadı (Mevcut: %d).",
					cfg.BuyQty, cfg.ItemCode, qty)
			}

		case "COMBO":
			var cfg struct {
				RequiredCodes []string `json:"required_codes"`
				DiscountKurus int64    `json:"discount_kurus"`
			}
			_ = json.Unmarshal(c.ConfigJSON, &cfg)
			if len(cfg.RequiredCodes) > 0 && cfg.DiscountKurus > 0 {
				comboCount := 999999
				for _, code := range cfg.RequiredCodes {
					if itemQtyByCode[code] < comboCount {
						comboCount = itemQtyByCode[code]
					}
				}
				if comboCount > 0 && comboCount < 999999 {
					disc := int64(comboCount) * cfg.DiscountKurus
					cand.eligible = true
					cand.discount = disc
					cand.reason = fmt.Sprintf("Kombinasyon menü kalemleri (%v) sepette %d set olarak eşleşti; %s TL kombo indirimi kazanıldı.",
						cfg.RequiredCodes, comboCount, formatKurus(disc))
				} else {
					cand.reason = fmt.Sprintf("Kombinasyon menü için gerekli tüm ürünler (%v) sepette bir arada bulunmuyor.", cfg.RequiredCodes)
				}
			}

		case "HAPPY_HOUR":
			var cfg struct {
				DiscountPct int64 `json:"discount_pct"`
				StartHour   int   `json:"start_hour"`
				EndHour     int   `json:"end_hour"`
			}
			_ = json.Unmarshal(c.ConfigJSON, &cfg)
			inWindow := currentHour >= cfg.StartHour && currentHour < cfg.EndHour
			if cfg.StartHour > cfg.EndHour {
				inWindow = currentHour >= cfg.StartHour || currentHour < cfg.EndHour
			}
			if inWindow && cfg.DiscountPct > 0 && cfg.DiscountPct <= 100 {
				disc := (subtotal * cfg.DiscountPct) / 100
				cand.eligible = true
				cand.discount = disc
				cand.reason = fmt.Sprintf("Saat aralığı (%02d:00-%02d:00) içinde %%%d saatlik indirim (%s TL) hesaplandı.",
					cfg.StartHour, cfg.EndHour, cfg.DiscountPct, formatKurus(disc))
			} else {
				cand.reason = fmt.Sprintf("Saatlik indirim zaman dilimi (%02d:00-%02d:00) dışında.", cfg.StartHour, cfg.EndHour)
			}
		}

		candidates = append(candidates, cand)
	}

	if coupon != nil && coupon.Code != "" {
		cand := candidateEval{
			code:      coupon.Code,
			name:      "Kupon: " + coupon.Code,
			ruleType:  "COUPON",
			stackable: coupon.Stackable,
		}
		if !coupon.IsActive {
			cand.reason = "Kupon aktif değil."
		} else if subtotal < coupon.MinOrderKurus {
			cand.reason = fmt.Sprintf("Kupon minimum sepet tutarı (%s TL) sağlanmadı (Sepet: %s TL).",
				formatKurus(coupon.MinOrderKurus), formatKurus(subtotal))
		} else {
			var disc int64
			if coupon.DiscountType == "FIXED" {
				disc = coupon.DiscountValue
			} else if coupon.DiscountType == "PERCENT" {
				disc = (subtotal * coupon.DiscountValue) / 100
			}
			if coupon.MaxDiscountKurus > 0 && disc > coupon.MaxDiscountKurus {
				disc = coupon.MaxDiscountKurus
			}
			cand.eligible = true
			cand.discount = disc
			cand.reason = fmt.Sprintf("'%s' kupon koşulu sağlandı (%s TL indirim).", coupon.Code, formatKurus(disc))
		}
		candidates = append(candidates, cand)
	}

	// Strategy A: Combine all eligible stackable campaigns/coupons
	var stackableTotal int64
	stackableSet := make(map[string]bool)
	for _, cand := range candidates {
		if cand.eligible && cand.stackable {
			stackableTotal += cand.discount
			stackableSet[cand.code] = true
		}
	}
	if stackableTotal > subtotal {
		stackableTotal = subtotal
	}

	// Strategy B: Best single non-stackable campaign/coupon
	var bestExclusiveCode string
	var bestExclusiveDiscount int64
	for _, cand := range candidates {
		if cand.eligible && !cand.stackable {
			d := cand.discount
			if d > subtotal {
				d = subtotal
			}
			if d > bestExclusiveDiscount {
				bestExclusiveDiscount = d
				bestExclusiveCode = cand.code
			}
		}
	}

	useStackable := stackableTotal >= bestExclusiveDiscount
	winningDiscount := stackableTotal
	winningStrategy := "STACKABLE_BUNDLE"
	if !useStackable {
		winningDiscount = bestExclusiveDiscount
		winningStrategy = "EXCLUSIVE_SINGLE (" + bestExclusiveCode + ")"
	}
	if winningDiscount == 0 {
		winningStrategy = "NO_DISCOUNT"
	}

	var applied []CampaignDecision
	remainingDiscount := winningDiscount
	all := make([]CampaignDecision, 0, len(candidates))

	for _, cand := range candidates {
		dec := CampaignDecision{
			Code:          cand.code,
			Name:          cand.name,
			RuleType:      cand.ruleType,
			DiscountKurus: cand.discount,
			Reason:        cand.reason,
		}
		if !cand.eligible {
			dec.Applied = false
		} else if useStackable && cand.stackable {
			dec.Applied = true
			dec.DiscountKurus = min(cand.discount, remainingDiscount)
			remainingDiscount -= dec.DiscountKurus
			dec.Reason = cand.reason + fmt.Sprintf(" [Birleştirilebilir paket toplamı (%s TL) en avantajlı seçenek olduğu için uygulandı]", formatKurus(winningDiscount))
			applied = append(applied, dec)
		} else if !useStackable && cand.code == bestExclusiveCode {
			dec.Applied = true
			dec.DiscountKurus = winningDiscount
			dec.Reason = cand.reason + fmt.Sprintf(" [Tekil kampanya (%s TL), birleştirilebilir paketten (%s TL) daha avantajlı olduğu için seçildi]", formatKurus(winningDiscount), formatKurus(stackableTotal))
			applied = append(applied, dec)
		} else {
			dec.Applied = false
			dec.Reason = cand.reason + fmt.Sprintf(" [Birlikte kullanılamaz kuralı: Müşteri için daha avantajlı olan %s TL indirimli kombinasyon tercih edildi]", formatKurus(winningDiscount))
		}
		all = append(all, dec)
	}

	return CampaignEvaluationResult{
		SubtotalKurus:      subtotal,
		TotalDiscountKurus: winningDiscount,
		NetTotalKurus:      subtotal - winningDiscount,
		WinningStrategy:    winningStrategy,
		AppliedCampaigns:   applied,
		AllEvaluations:     all,
	}
}

func formatKurus(k int64) string {
	return fmt.Sprintf("%d.%02d", k/100, k%100)
}
