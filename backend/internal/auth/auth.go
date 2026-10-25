package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

const (
	RoleWaiter        = "waiter"
	RoleCashier       = "cashier"
	RoleChef          = "chef"
	RoleBranchManager = "branch_manager"
	RoleHQManager     = "hq_manager"
	RoleCustomer      = "customer"
)

// AllRoles lists all valid SofraPOS roles.
var AllRoles = []string{
	RoleWaiter,
	RoleCashier,
	RoleChef,
	RoleBranchManager,
	RoleHQManager,
	RoleCustomer,
}

// Claims represents the JWT payload for SofraPOS users.
type Claims struct {
	UserID    int64  `json:"sub"`
	BranchID  int64  `json:"branch_id"`
	Email     string `json:"email"`
	FullName  string `json:"name"`
	Role      string `json:"role"`
	TokenType string `json:"token_type"` // "access" or "refresh"
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// TokenPair holds access and refresh JWTs.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// ErrorPayload is the standardized API error structure {"error":{"code":"...","message":"..."}}.
type ErrorPayload struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail holds machine-readable code and human-readable message.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RespondError writes a standardized JSON error response.
func RespondError(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(ErrorPayload{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}

// HashPassword hashes a plaintext password using bcrypt.
func HashPassword(password string) (string, error) {
	if len(password) < 6 {
		return "", errors.New("password must be at least 6 characters")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword compares a bcrypt hashed password with plaintext password.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// GenerateTokenPair generates an access token (2h) and refresh token (7d).
func GenerateTokenPair(secret string, userID, branchID int64, email, fullName, role string) (TokenPair, error) {
	now := time.Now().UTC()
	accessClaims := Claims{
		UserID:    userID,
		BranchID:  branchID,
		Email:     email,
		FullName:  fullName,
		Role:      role,
		TokenType: "access",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(2 * time.Hour).Unix(),
	}
	refreshClaims := Claims{
		UserID:    userID,
		BranchID:  branchID,
		Email:     email,
		FullName:  fullName,
		Role:      role,
		TokenType: "refresh",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(7 * 24 * time.Hour).Unix(),
	}

	access, err := SignJWT(secret, accessClaims)
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := SignJWT(secret, refreshClaims)
	if err != nil {
		return TokenPair{}, err
	}

	return TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    7200,
		TokenType:    "Bearer",
	}, nil
}

// SignJWT creates an HS256 signed JWT string.
func SignJWT(secret string, claims Claims) (string, error) {
	headerJSON := `{"alg":"HS256","typ":"JWT"}`
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	hEnc := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))
	pEnc := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signingInput := hEnc + "." + pEnc

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sig, nil
}

// VerifyJWT validates the HS256 signature and expiration of a JWT string.
func VerifyJWT(secret, token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("malformed token")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, errors.New("invalid token header")
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Alg != "HS256" || header.Typ != "JWT" {
		return Claims{}, errors.New("unsupported token header")
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return Claims{}, errors.New("invalid token signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("decode payload: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return Claims{}, fmt.Errorf("unmarshal claims: %w", err)
	}

	if time.Now().UTC().Unix() >= claims.ExpiresAt || claims.UserID <= 0 || claims.BranchID <= 0 {
		return Claims{}, errors.New("token expired")
	}

	return claims, nil
}

const claimsContextKey = "sofrapos_claims"

// RequireAuth middleware verifies Bearer token (or ?token= for SSE) and stores Claims in Fiber context.
func RequireAuth(secret string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		rawToken := ""
		authHeader := c.Get(fiber.HeaderAuthorization)
		if strings.HasPrefix(authHeader, "Bearer ") {
			rawToken = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		}
		if rawToken == "" && c.Path() == "/api/v1/kds/stream" {
			rawToken = strings.TrimSpace(c.Query("token"))
		}
		if rawToken == "" {
			return RespondError(c, fiber.StatusUnauthorized, "UNAUTHORIZED", "Kimlik doğrulama başlığı (Bearer token) eksik.")
		}

		claims, err := VerifyJWT(secret, rawToken)
		if err != nil || claims.TokenType != "access" {
			return RespondError(c, fiber.StatusUnauthorized, "INVALID_TOKEN", "Geçersiz veya süresi dolmuş erişim anahtarı.")
		}

		c.Locals(claimsContextKey, claims)
		return c.Next()
	}
}

// RequireRoles middleware enforces role-based access control (RBAC) on an endpoint.
func RequireRoles(allowedRoles ...string) fiber.Handler {
	roleSet := make(map[string]struct{}, len(allowedRoles))
	for _, r := range allowedRoles {
		roleSet[r] = struct{}{}
	}

	return func(c *fiber.Ctx) error {
		claims, ok := GetClaims(c)
		if !ok {
			return RespondError(c, fiber.StatusUnauthorized, "UNAUTHORIZED", "Oturum bilgisi bulunamadı.")
		}

		if _, allowed := roleSet[claims.Role]; !allowed {
			return RespondError(c, fiber.StatusForbidden, "FORBIDDEN",
				fmt.Sprintf("Bu işlem için yetkiniz yok (Mevcut rol: %s).", claims.Role))
		}

		return c.Next()
	}
}

// GetClaims retrieves authenticated user claims from the Fiber context.
func GetClaims(c *fiber.Ctx) (Claims, bool) {
	val := c.Locals(claimsContextKey)
	if val == nil {
		return Claims{}, false
	}
	claims, ok := val.(Claims)
	return claims, ok
}
