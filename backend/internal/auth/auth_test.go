package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/efehidir/ddo/backend/internal/auth"
)

const testSecret = "unit-test-secret-key-2026"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := auth.HashPassword("Sofra2026!")
	if err != nil {
		t.Fatalf("unexpected error hashing password: %v", err)
	}
	if !auth.VerifyPassword(hash, "Sofra2026!") {
		t.Errorf("expected password verification to succeed")
	}
	if auth.VerifyPassword(hash, "WrongPassword") {
		t.Errorf("expected wrong password verification to fail")
	}
}

func TestHashPassword_TooShort(t *testing.T) {
	if _, err := auth.HashPassword("123"); err == nil {
		t.Errorf("expected error for short password")
	}
}

func TestGenerateAndVerifyTokenPair(t *testing.T) {
	pair, err := auth.GenerateTokenPair(testSecret, 10, 1, "garson@sofrapos.local", "Ahmet", auth.RoleWaiter)
	if err != nil {
		t.Fatalf("unexpected error generating token pair: %v", err)
	}

	claims, err := auth.VerifyJWT(testSecret, pair.AccessToken)
	if err != nil {
		t.Fatalf("unexpected error verifying access token: %v", err)
	}
	if claims.UserID != 10 || claims.Role != auth.RoleWaiter || claims.TokenType != "access" {
		t.Errorf("unexpected claims: %+v", claims)
	}

	refreshClaims, err := auth.VerifyJWT(testSecret, pair.RefreshToken)
	if err != nil {
		t.Fatalf("unexpected error verifying refresh token: %v", err)
	}
	if refreshClaims.TokenType != "refresh" {
		t.Errorf("expected refresh token type, got %q", refreshClaims.TokenType)
	}
}

func TestVerifyJWT_TamperedOrExpired(t *testing.T) {
	pair, _ := auth.GenerateTokenPair(testSecret, 1, 1, "a@b.c", "User", auth.RoleCashier)

	if _, err := auth.VerifyJWT("wrong-secret", pair.AccessToken); err == nil {
		t.Errorf("expected error with wrong secret")
	}

	if _, err := auth.VerifyJWT(testSecret, "not-a-jwt"); err == nil {
		t.Errorf("expected error with malformed token")
	}

	expiredToken, _ := auth.SignJWT(testSecret, auth.Claims{
		UserID:    1,
		Role:      auth.RoleCashier,
		TokenType: "access",
		IssuedAt:  time.Now().Add(-2 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-1 * time.Hour).Unix(),
	})
	if _, err := auth.VerifyJWT(testSecret, expiredToken); err == nil {
		t.Errorf("expected error for expired token")
	}
}

func TestRBACMiddleware_401And403And200(t *testing.T) {
	app := fiber.New()
	app.Get("/cashier-only",
		auth.RequireAuth(testSecret),
		auth.RequireRoles(auth.RoleCashier, auth.RoleBranchManager),
		func(c *fiber.Ctx) error {
			claims, _ := auth.GetClaims(c)
			return c.JSON(fiber.Map{"user": claims.Email})
		},
	)

	// 1. Missing token -> 401
	req1 := httptest.NewRequest(http.MethodGet, "/cashier-only", nil)
	resp1, _ := app.Test(req1, -1)
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp1.StatusCode)
	}

	// 2. Waiter token on cashier-only endpoint -> 403
	waiterPair, _ := auth.GenerateTokenPair(testSecret, 2, 1, "garson@sofrapos.local", "Ahmet", auth.RoleWaiter)
	req2 := httptest.NewRequest(http.MethodGet, "/cashier-only", nil)
	req2.Header.Set("Authorization", "Bearer "+waiterPair.AccessToken)
	resp2, _ := app.Test(req2, -1)
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for waiter on cashier endpoint, got %d", resp2.StatusCode)
	}

	// 3. Cashier token -> 200
	cashierPair, _ := auth.GenerateTokenPair(testSecret, 3, 1, "kasiyer@sofrapos.local", "Zeynep", auth.RoleCashier)
	req3 := httptest.NewRequest(http.MethodGet, "/cashier-only", nil)
	req3.Header.Set("Authorization", "Bearer "+cashierPair.AccessToken)
	resp3, _ := app.Test(req3, -1)
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for cashier, got %d", resp3.StatusCode)
	}

	// 4. Refresh token used as access token -> 401
	req4 := httptest.NewRequest(http.MethodGet, "/cashier-only", nil)
	req4.Header.Set("Authorization", "Bearer "+cashierPair.RefreshToken)
	resp4, _ := app.Test(req4, -1)
	if resp4.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 when using refresh token as access token, got %d", resp4.StatusCode)
	}

	// Query tokens are accepted only by the EventSource endpoint.
	req5 := httptest.NewRequest(http.MethodGet, "/cashier-only?token="+cashierPair.AccessToken, nil)
	resp5, _ := app.Test(req5, -1)
	if resp5.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 with query token outside SSE, got %d", resp5.StatusCode)
	}
	app.Get("/api/v1/kds/stream", auth.RequireAuth(testSecret), func(c *fiber.Ctx) error { return c.SendStatus(200) })
	sse, _ := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/kds/stream?token="+cashierPair.AccessToken, nil), -1)
	defer sse.Body.Close()
	if sse.StatusCode != 200 {
		t.Errorf("SSE query token rejected: %d", sse.StatusCode)
	}
}
