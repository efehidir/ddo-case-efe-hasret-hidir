package domain

import (
	"errors"
	"fmt"
	"sort"
)

// RecipeEdge represents a single row in "dz-recipes".
type RecipeEdge struct {
	MenuItemID       int64  `json:"menu_item_id"`
	SemiFinishedCode string `json:"semi_finished_code"`
	IngredientCode   string `json:"ingredient_code"`
	QtyGrams         int64  `json:"qty_grams"`
}

// ModifierRecipeEffect represents an optional modifier that adds extra ingredient usage.
type ModifierRecipeEffect struct {
	Name           string `json:"name"`
	IngredientCode string `json:"ingredient_code"`
	ExtraQtyGrams  int64  `json:"extra_qty_grams"`
}

// IngredientRequirement represents the resolved raw ingredient consumption in grams.
type IngredientRequirement struct {
	IngredientCode  string `json:"ingredient_code"`
	TotalGrams      int64  `json:"total_grams"`
	FromSubRecipe   bool   `json:"from_sub_recipe"`
	SubRecipeSource string `json:"sub_recipe_source,omitempty"`
}

// ResolveRecipeTree expands a menu item's recipe (including nested semi-finished sub-recipes),
// applies the variant percentage multiplier (e.g. 150 for 1.5 portion), adds modifier extra grams,
// and scales by order quantity.
func ResolveRecipeTree(
	menuItemID int64,
	quantity int,
	variantMultiplierPct int,
	modifiers []ModifierRecipeEffect,
	edges []RecipeEdge,
) ([]IngredientRequirement, error) {
	if quantity <= 0 {
		return nil, errors.New("quantity must be greater than zero")
	}
	if variantMultiplierPct <= 0 {
		variantMultiplierPct = 100
	}

	subRecipes := make(map[string][]RecipeEdge)
	var itemEdges []RecipeEdge
	for _, e := range edges {
		if e.MenuItemID == menuItemID && e.SemiFinishedCode == "" {
			itemEdges = append(itemEdges, e)
		} else if e.SemiFinishedCode != "" {
			subRecipes[e.SemiFinishedCode] = append(subRecipes[e.SemiFinishedCode], e)
		}
	}

	totals := make(map[string]int64)
	fromSub := make(map[string]string)

	if len(itemEdges) == 0 {
		return nil, errors.New("product recipe is missing")
	}
	path := map[string]bool{}
	var expand func(string, int64, string) error
	expand = func(code string, grams int64, source string) error {
		children, semi := subRecipes[code]
		if !semi {
			if len(code) >= 5 && code[:5] == "SEMI-" {
				return fmt.Errorf("missing sub-recipe %s", code)
			}
			totals[code] += grams
			if source != "" {
				fromSub[code] = source
			}
			return nil
		}
		if path[code] {
			return fmt.Errorf("recipe cycle at %s", code)
		}
		path[code] = true
		defer delete(path, code)
		var weight int64
		for _, child := range children {
			if child.QtyGrams <= 0 {
				return errors.New("recipe quantities must be positive")
			}
			weight += child.QtyGrams
		}
		// Largest remainder keeps every gram when a batch is scaled to a portion.
		type part struct {
			code             string
			grams, remainder int64
		}
		parts := make([]part, 0, len(children))
		var assigned int64
		for _, child := range children {
			n := grams * child.QtyGrams
			parts = append(parts, part{child.IngredientCode, n / weight, n % weight})
			assigned += n / weight
		}
		sort.SliceStable(parts, func(i, j int) bool {
			if parts[i].remainder != parts[j].remainder {
				return parts[i].remainder > parts[j].remainder
			}
			return parts[i].code < parts[j].code
		})
		for i := int64(0); i < grams-assigned; i++ {
			parts[i].grams++
		}
		for _, part := range parts {
			if err := expand(part.code, part.grams, code); err != nil {
				return err
			}
		}
		return nil
	}
	for _, edge := range itemEdges {
		if edge.QtyGrams <= 0 {
			return nil, errors.New("recipe quantities must be positive")
		}
		scaled := (edge.QtyGrams * int64(variantMultiplierPct) * int64(quantity)) / 100
		if err := expand(edge.IngredientCode, scaled, ""); err != nil {
			return nil, err
		}
	}

	for _, mod := range modifiers {
		if mod.IngredientCode != "" {
			if mod.ExtraQtyGrams < 0 {
				// Removal scales with the portion, while an extra is a fixed serving.
				totals[mod.IngredientCode] += mod.ExtraQtyGrams * int64(quantity) * int64(variantMultiplierPct) / 100
			} else {
				totals[mod.IngredientCode] += mod.ExtraQtyGrams * int64(quantity)
			}
		}
	}

	result := make([]IngredientRequirement, 0, len(totals))
	for code, grams := range totals {
		if grams <= 0 {
			continue
		}
		subSource := fromSub[code]
		result = append(result, IngredientRequirement{
			IngredientCode:  code,
			TotalGrams:      grams,
			FromSubRecipe:   subSource != "",
			SubRecipeSource: subSource,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].IngredientCode < result[j].IngredientCode
	})

	return result, nil
}

// StockCountInput represents a single ingredient's theoretical vs counted stock at end of day.
type StockCountInput struct {
	IngredientID     int64  `json:"ingredient_id"`
	IngredientCode   string `json:"ingredient_code"`
	IngredientName   string `json:"ingredient_name"`
	Unit             string `json:"unit"`
	TheoreticalGrams int64  `json:"theoretical_grams"`
	CountedGrams     int64  `json:"counted_grams"`
	CostPerUnitKurus int64  `json:"cost_per_unit_kurus"`
}

// StockVarianceRow represents the variance calculation for a single ingredient.
type StockVarianceRow struct {
	IngredientID      int64   `json:"ingredient_id"`
	IngredientCode    string  `json:"ingredient_code"`
	IngredientName    string  `json:"ingredient_name"`
	Unit              string  `json:"unit"`
	TheoreticalGrams  int64   `json:"theoretical_grams"`
	CountedGrams      int64   `json:"counted_grams"`
	VarianceGrams     int64   `json:"variance_grams"`
	VarianceCostKurus int64   `json:"variance_cost_kurus"`
	VariancePct       float64 `json:"variance_pct"`
	Status            string  `json:"status"` // "MATCH", "DEFICIT_WASTE", "SURPLUS"
}

// VarianceReport holds all rows and aggregate deficit/surplus totals in kurus.
type VarianceReport struct {
	Rows                  []StockVarianceRow `json:"rows"`
	TotalDeficitGrams     int64              `json:"total_deficit_grams"`
	TotalDeficitCostKurus int64              `json:"total_deficit_cost_kurus"`
	TotalSurplusGrams     int64              `json:"total_surplus_grams"`
	NetVarianceCostKurus  int64              `json:"net_variance_cost_kurus"`
}

// ComputeStockVarianceReport calculates the exact difference between end-of-day physical count
// and theoretical stock per ingredient.
func ComputeStockVarianceReport(inputs []StockCountInput) VarianceReport {
	report := VarianceReport{
		Rows: make([]StockVarianceRow, 0, len(inputs)),
	}

	for _, in := range inputs {
		variance := in.CountedGrams - in.TheoreticalGrams
		// Cost per unit is stored per 100g/unit or per gram; we compute proportional integer kurus
		varianceCost := variance * in.CostPerUnitKurus
		status := "MATCH"
		if variance < 0 {
			status = "DEFICIT_WASTE"
			report.TotalDeficitGrams += -variance
			report.TotalDeficitCostKurus += -varianceCost
		} else if variance > 0 {
			status = "SURPLUS"
			report.TotalSurplusGrams += variance
		}
		report.NetVarianceCostKurus += varianceCost

		var pct float64
		if in.TheoreticalGrams > 0 {
			pct = float64(variance*10000/in.TheoreticalGrams) / 100.0
		}

		report.Rows = append(report.Rows, StockVarianceRow{
			IngredientID:      in.IngredientID,
			IngredientCode:    in.IngredientCode,
			IngredientName:    in.IngredientName,
			Unit:              in.Unit,
			TheoreticalGrams:  in.TheoreticalGrams,
			CountedGrams:      in.CountedGrams,
			VarianceGrams:     variance,
			VarianceCostKurus: varianceCost,
			VariancePct:       pct,
			Status:            status,
		})
	}

	return report
}
