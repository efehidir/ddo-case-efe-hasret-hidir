package server

import (
	"github.com/efehidir/ddo/backend/internal/auth"
	"testing"
)

func TestBranchAccess(t *testing.T) {
	for _, role := range auth.AllRoles {
		t.Run(role, func(t *testing.T) {
			claims := auth.Claims{Role: role, BranchID: 1}
			if !canAccessBranch(claims, 1) {
				t.Fatal("own branch denied")
			}
			if canAccessBranch(claims, 2) != (role == auth.RoleHQManager) {
				t.Fatal("cross-branch authorization mismatch")
			}
		})
	}
}

func TestIdempotencyIdentityScope(t *testing.T) {
	base := requestIdentity(1, "POST", "/orders/1/payments", "key")
	for _, other := range []string{
		requestIdentity(2, "POST", "/orders/1/payments", "key"),
		requestIdentity(1, "PATCH", "/orders/1/payments", "key"),
		requestIdentity(1, "POST", "/orders/2/payments", "key"),
		requestIdentity(1, "POST", "/orders/1/payments", "other"),
	} {
		if base == other {
			t.Fatal("different request scopes share an identity")
		}
	}
	if base != requestIdentity(1, "POST", "/orders/1/payments", "key") {
		t.Fatal("identity is not deterministic")
	}
}
