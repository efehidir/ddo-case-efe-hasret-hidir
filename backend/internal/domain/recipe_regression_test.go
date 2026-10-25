package domain_test

import (
	"github.com/efehidir/ddo/backend/internal/domain"
	"testing"
)

func TestNestedRecipePreservesEveryGram(t *testing.T) {
	edges := []domain.RecipeEdge{
		{MenuItemID: 1, IngredientCode: "SEMI-A", QtyGrams: 101},
		{SemiFinishedCode: "SEMI-A", IngredientCode: "SEMI-B", QtyGrams: 2},
		{SemiFinishedCode: "SEMI-A", IngredientCode: "RAW-C", QtyGrams: 1},
		{SemiFinishedCode: "SEMI-B", IngredientCode: "RAW-A", QtyGrams: 1},
		{SemiFinishedCode: "SEMI-B", IngredientCode: "RAW-B", QtyGrams: 1},
	}
	result, err := domain.ResolveRecipeTree(1, 1, 100, nil, edges)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, r := range result {
		sum += r.TotalGrams
		if !r.FromSubRecipe {
			t.Fatal("nested raw ingredient lost origin")
		}
	}
	if sum != 101 {
		t.Fatalf("lost grams: %d", sum)
	}
}

func TestRecipeCycleRejected(t *testing.T) {
	edges := []domain.RecipeEdge{
		{MenuItemID: 1, IngredientCode: "SEMI-A", QtyGrams: 100},
		{SemiFinishedCode: "SEMI-A", IngredientCode: "SEMI-B", QtyGrams: 1},
		{SemiFinishedCode: "SEMI-B", IngredientCode: "SEMI-A", QtyGrams: 1},
	}
	if _, err := domain.ResolveRecipeTree(1, 1, 100, nil, edges); err == nil {
		t.Fatal("cycle accepted")
	}
	if _, err := domain.ResolveRecipeTree(999, 1, 100, nil, edges); err == nil {
		t.Fatal("missing recipe accepted")
	}
}

func TestRemovalModifierScalesWithVariant(t *testing.T) {
	edges := []domain.RecipeEdge{{MenuItemID: 1, IngredientCode: "YOGURT", QtyGrams: 80}, {MenuItemID: 1, IngredientCode: "BEEF", QtyGrams: 180}}
	result, err := domain.ResolveRecipeTree(1, 2, 150, []domain.ModifierRecipeEffect{{IngredientCode: "YOGURT", ExtraQtyGrams: -80}}, edges)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].IngredientCode != "BEEF" || result[0].TotalGrams != 540 {
		t.Fatalf("removal ignored: %+v", result)
	}
}
