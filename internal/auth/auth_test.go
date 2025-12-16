package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestGenerateAPIKey(t *testing.T) {
	key1 := generateAPIKey()
	key2 := generateAPIKey()

	// Should be 64 chars (32 bytes hex)
	if len(key1) != 64 {
		t.Errorf("API key should be 64 chars, got %d", len(key1))
	}

	// Should be unique
	if key1 == key2 {
		t.Error("API keys should be unique")
	}
}

func TestHashPassword(t *testing.T) {
	password := "testpassword123"

	hash, err := hashPassword(password)
	if err != nil {
		t.Fatalf("hashPassword failed: %v", err)
	}

	// Hash should be different from password
	if hash == password {
		t.Error("Hash should be different from password")
	}

	// Should start with $2a$ (bcrypt prefix)
	if len(hash) < 4 || hash[:4] != "$2a$" {
		t.Error("Hash should be bcrypt format")
	}
}

func TestCheckPassword(t *testing.T) {
	password := "testpassword123"
	hash, _ := hashPassword(password)

	tests := []struct {
		name     string
		password string
		hash     string
		expected bool
	}{
		{"correct password", password, hash, true},
		{"wrong password", "wrongpassword", hash, false},
		{"empty password", "", hash, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkPassword(tt.password, tt.hash); got != tt.expected {
				t.Errorf("checkPassword() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHandler_GenerateAndValidateToken(t *testing.T) {
	h := &Handler{
		jwtSecret: []byte("test-secret-key"),
	}

	user := &User{
		ID:    "user-123",
		Email: "test@example.com",
		Role:  "user",
	}

	// Generate token
	token, err := h.generateToken(user)
	if err != nil {
		t.Fatalf("generateToken failed: %v", err)
	}

	if token == "" {
		t.Error("Token should not be empty")
	}

	// Validate token
	claims, err := h.validateToken(token)
	if err != nil {
		t.Fatalf("validateToken failed: %v", err)
	}

	if claims.UserID != user.ID {
		t.Errorf("UserID = %s, want %s", claims.UserID, user.ID)
	}
	if claims.Email != user.Email {
		t.Errorf("Email = %s, want %s", claims.Email, user.Email)
	}
	if claims.Role != user.Role {
		t.Errorf("Role = %s, want %s", claims.Role, user.Role)
	}
}

func TestHandler_ValidateToken_Invalid(t *testing.T) {
	h := &Handler{
		jwtSecret: []byte("test-secret-key"),
	}

	tests := []struct {
		name  string
		token string
	}{
		{"empty token", ""},
		{"invalid format", "not-a-jwt"},
		{"wrong secret", createTokenWithSecret("wrong-secret", "user-123")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.validateToken(tt.token)
			if err == nil {
				t.Error("Expected error for invalid token")
			}
		})
	}
}

func TestHandler_ValidateToken_Expired(t *testing.T) {
	h := &Handler{
		jwtSecret: []byte("test-secret-key"),
	}

	// Create expired token
	claims := Claims{
		UserID: "user-123",
		Email:  "test@example.com",
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString(h.jwtSecret)

	_, err := h.validateToken(tokenString)
	if err == nil {
		t.Error("Expected error for expired token")
	}
}

func TestHandler_DefaultRole(t *testing.T) {
	h := &Handler{
		jwtSecret: []byte("test-secret-key"),
	}

	// User without role
	user := &User{
		ID:    "user-123",
		Email: "test@example.com",
		Role:  "", // empty role
	}

	token, _ := h.generateToken(user)
	claims, _ := h.validateToken(token)

	if claims.Role != "user" {
		t.Errorf("Default role should be 'user', got %s", claims.Role)
	}
}

// Helper to create token with different secret
func createTokenWithSecret(secret, userID string) string {
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte(secret))
	return tokenString
}

func TestHandleLogin_MethodNotAllowed(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	w := httptest.NewRecorder()

	h.HandleLogin(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleRegister_MethodNotAllowed(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/register", nil)
	w := httptest.NewRecorder()

	h.HandleRegister(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleMe_MethodNotAllowed(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/me", nil)
	w := httptest.NewRecorder()

	h.HandleMe(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleMe_NoAuth(t *testing.T) {
	h := &Handler{
		jwtSecret: []byte("test-secret"),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	w := httptest.NewRecorder()

	h.HandleMe(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleGoogleLogin_NotConfigured(t *testing.T) {
	h := &Handler{
		googleClientID: "", // not configured
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google", nil)
	w := httptest.NewRecorder()

	h.HandleGoogleLogin(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleGoogleVerify_MethodNotAllowed(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/verify", nil)
	w := httptest.NewRecorder()

	h.HandleGoogleVerify(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleGoogleVerify_EmptyEmail(t *testing.T) {
	h := &Handler{}

	payload := map[string]string{"email": ""}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/google/verify", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.HandleGoogleVerify(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSyncUser_MethodNotAllowed(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, "/sync/user", nil)
	w := httptest.NewRecorder()

	h.HandleSyncUser(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleSyncUser_InvalidSecret(t *testing.T) {
	h := &Handler{
		webhookSecret: "correct-secret",
	}

	payload := map[string]string{"email": "test@example.com"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/sync/user", bytes.NewReader(body))
	req.Header.Set("X-Service-Secret", "wrong-secret")
	w := httptest.NewRecorder()

	h.HandleSyncUser(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleGetProjects_MethodNotAllowed(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest(http.MethodPost, "/api/projects", nil)
	w := httptest.NewRecorder()

	h.HandleGetProjects(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleCreateProject_MethodNotAllowed(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	w := httptest.NewRecorder()

	h.HandleCreateProject(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleDeleteProject_MethodNotAllowed(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest(http.MethodPost, "/api/projects", nil)
	w := httptest.NewRecorder()

	h.HandleDeleteProject(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"key": "value"}

	writeJSON(w, data, http.StatusCreated)

	if w.Code != http.StatusCreated {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusCreated)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %s, want application/json", w.Header().Get("Content-Type"))
	}
}

func TestIsAdmin(t *testing.T) {
	h := &Handler{
		jwtSecret: []byte("test-secret"),
	}

	// Create admin token
	adminClaims := Claims{
		UserID: "admin-123",
		Email:  "admin@example.com",
		Role:   "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	adminToken := jwt.NewWithClaims(jwt.SigningMethodHS256, adminClaims)
	adminTokenString, _ := adminToken.SignedString(h.jwtSecret)

	// Create user token
	userClaims := Claims{
		UserID: "user-123",
		Email:  "user@example.com",
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	userToken := jwt.NewWithClaims(jwt.SigningMethodHS256, userClaims)
	userTokenString, _ := userToken.SignedString(h.jwtSecret)

	tests := []struct {
		name     string
		token    string
		expected bool
	}{
		{"admin user", adminTokenString, true},
		{"regular user", userTokenString, false},
		{"no token", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}

			if got := h.isAdmin(req); got != tt.expected {
				t.Errorf("isAdmin() = %v, want %v", got, tt.expected)
			}
		})
	}
}
