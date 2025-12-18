package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	db                 *DB
	jwtSecret          []byte
	webhookSecret      string
	syncSecret         string
	googleClientID     string
	googleClientSecret string
	googleRedirectURL  string
	frontendURL        string
}

func NewHandler(db *DB, jwtSecret, webhookSecret, googleClientID, googleClientSecret, googleRedirectURL, frontendURL string) *Handler {
	if db == nil {
		return nil
	}
	return &Handler{
		db:                 db,
		jwtSecret:          []byte(jwtSecret),
		webhookSecret:      webhookSecret,
		syncSecret:         webhookSecret, // reuse webhook secret for sync
		googleClientID:     googleClientID,
		googleClientSecret: googleClientSecret,
		googleRedirectURL:  googleRedirectURL,
		frontendURL:        frontendURL,
	}
}

// JWT claims
type Claims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// Request/Response types
type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name,omitempty"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token string `json:"token"`
	User  *User  `json:"user"`
}

// Sync payload for cross-service user sync
type SyncUserPayload struct {
	Email        string `json:"email"`
	Name         string `json:"name,omitempty"`
	PhotoURL     string `json:"photo_url,omitempty"`
	Energy       int    `json:"energy"`
	IsSubscribed bool   `json:"is_subscribed"`
	HasPurchased bool   `json:"has_purchased"`
}

// Sync URLs for other services
var syncURLs = []string{
	"https://api.woopicx.com/sync/user",
	"https://api.shortodella.com/sync/user",
}

// Helper functions
func generateAPIKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func checkPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (h *Handler) generateToken(user *User) (string, error) {
	role := user.Role
	if role == "" {
		role = "user"
	}
	claims := Claims{
		UserID: user.ID,
		Email:  user.Email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(h.jwtSecret)
}

func (h *Handler) validateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return h.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

func (h *Handler) getUserFromRequest(r *http.Request) (*User, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, fmt.Errorf("no authorization header")
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return nil, fmt.Errorf("invalid authorization header")
	}

	claims, err := h.validateToken(parts[1])
	if err != nil {
		return nil, err
	}

	return h.db.GetUserByID(claims.UserID)
}

// Sync user to other services (Woopicx, Shortodella)
func (h *Handler) syncUserToOthers(user *User) {
	var name string
	if user.Name != nil {
		name = *user.Name
	}

	totalEnergy := user.PermanentEnergy + user.SubscriptionEnergy + user.DailyBonusEnergy

	payload := SyncUserPayload{
		Email:        user.Email,
		Name:         name,
		Energy:       totalEnergy,
		IsSubscribed: user.SubscriptionEnergy > 0,
		HasPurchased: user.PermanentEnergy > 0,
	}

	data, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 5 * time.Second}

	for _, url := range syncURLs {
		req, err := http.NewRequest("POST", url, bytes.NewReader(data))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Service-Secret", h.webhookSecret)

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("Warning: failed to sync to %s: %v\n", url, err)
			continue
		}
		resp.Body.Close()
	}
}

// Handlers
func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "Invalid request"}, http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.Password == "" {
		writeJSON(w, map[string]string{"error": "Email and password required"}, http.StatusBadRequest)
		return
	}

	// Check if user exists
	if _, err := h.db.GetUserByEmail(req.Email); err == nil {
		writeJSON(w, map[string]string{"error": "User already exists"}, http.StatusConflict)
		return
	}

	// Hash password
	passwordHash, err := hashPassword(req.Password)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to hash password"}, http.StatusInternalServerError)
		return
	}

	// Create user
	var name *string
	if req.Name != "" {
		name = &req.Name
	}

	user, err := h.db.CreateUser(req.Email, passwordHash, name, nil)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to create user"}, http.StatusInternalServerError)
		return
	}

	// Generate token
	token, err := h.generateToken(user)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to generate token"}, http.StatusInternalServerError)
		return
	}

	// Sync to other services
	go h.syncUserToOthers(user)

	writeJSON(w, AuthResponse{Token: token, User: user}, http.StatusCreated)
}

func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "Invalid request"}, http.StatusBadRequest)
		return
	}

	user, err := h.db.GetUserByEmail(req.Email)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Invalid credentials"}, http.StatusUnauthorized)
		return
	}

	if !checkPassword(req.Password, user.PasswordHash) {
		writeJSON(w, map[string]string{"error": "Invalid credentials"}, http.StatusUnauthorized)
		return
	}

	token, err := h.generateToken(user)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to generate token"}, http.StatusInternalServerError)
		return
	}

	writeJSON(w, AuthResponse{Token: token, User: user}, http.StatusOK)
}

func (h *Handler) HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := h.getUserFromRequest(r)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Unauthorized"}, http.StatusUnauthorized)
		return
	}

	writeJSON(w, user, http.StatusOK)
}

// Google OAuth types
type GoogleTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	IDToken     string `json:"id_token"`
}

type GoogleUserInfo struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

// HandleGoogleLogin - redirects to Google OAuth
func (h *Handler) HandleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	if h.googleClientID == "" {
		writeJSON(w, map[string]string{"error": "Google OAuth not configured"}, http.StatusInternalServerError)
		return
	}

	// Get redirect URL from query param (for local dev) or use default
	redirectURL := r.URL.Query().Get("redirect")
	if redirectURL == "" {
		redirectURL = h.frontendURL
	}

	// Encode redirect URL in state (base64)
	state := generateAPIKey()[:16] + ":" + redirectURL

	authURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=email%%20profile&state=%s&access_type=offline&prompt=select_account",
		url.QueryEscape(h.googleClientID),
		url.QueryEscape(h.googleRedirectURL),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// HandleGoogleCallback - handles Google OAuth callback
func (h *Handler) HandleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	// Extract redirect URL from state
	state := r.URL.Query().Get("state")
	frontendURL := h.frontendURL
	if parts := strings.SplitN(state, ":", 2); len(parts) == 2 {
		frontendURL = parts[1]
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, frontendURL+"/login?error=no_code", http.StatusTemporaryRedirect)
		return
	}

	// Exchange code for token
	tokenResp, err := http.PostForm("https://oauth2.googleapis.com/token", url.Values{
		"code":          {code},
		"client_id":     {h.googleClientID},
		"client_secret": {h.googleClientSecret},
		"redirect_uri":  {h.googleRedirectURL},
		"grant_type":    {"authorization_code"},
	})
	if err != nil {
		http.Redirect(w, r, frontendURL+"/login?error=token_exchange_failed", http.StatusTemporaryRedirect)
		return
	}
	defer tokenResp.Body.Close()

	body, _ := io.ReadAll(tokenResp.Body)
	var tokenData GoogleTokenResponse
	if err := json.Unmarshal(body, &tokenData); err != nil {
		http.Redirect(w, r, frontendURL+"/login?error=invalid_token_response", http.StatusTemporaryRedirect)
		return
	}

	// Get user info from Google
	userReq, _ := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	userResp, err := client.Do(userReq)
	if err != nil {
		http.Redirect(w, r, frontendURL+"/login?error=userinfo_failed", http.StatusTemporaryRedirect)
		return
	}
	defer userResp.Body.Close()

	body, _ = io.ReadAll(userResp.Body)
	var googleUser GoogleUserInfo
	if err := json.Unmarshal(body, &googleUser); err != nil {
		http.Redirect(w, r, frontendURL+"/login?error=invalid_userinfo", http.StatusTemporaryRedirect)
		return
	}

	// Find or create user
	user, err := h.db.GetUserByEmail(googleUser.Email)
	if err != nil {
		// User doesn't exist, create new one
		var name *string
		if googleUser.Name != "" {
			name = &googleUser.Name
		}
		user, err = h.db.CreateUser(googleUser.Email, "", name, nil)
		if err != nil {
			http.Redirect(w, r, frontendURL+"/login?error=create_user_failed", http.StatusTemporaryRedirect)
			return
		}
		// Sync new user to other services
		go h.syncUserToOthers(user)
	}

	// Generate JWT token
	token, err := h.generateToken(user)
	if err != nil {
		http.Redirect(w, r, frontendURL+"/login?error=token_generation_failed", http.StatusTemporaryRedirect)
		return
	}

	// Redirect to frontend with token
	http.Redirect(w, r, frontendURL+"/auth/callback?token="+token, http.StatusTemporaryRedirect)
}

// HandleGoogleVerify - verifies Google user and returns JWT (for Next.js frontend OAuth)
func (h *Handler) HandleGoogleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, map[string]string{"error": "Invalid request"}, http.StatusBadRequest)
		return
	}

	if payload.Email == "" {
		writeJSON(w, map[string]string{"error": "Email required"}, http.StatusBadRequest)
		return
	}

	// Find or create user
	user, err := h.db.GetUserByEmail(payload.Email)
	if err != nil {
		// User doesn't exist, create new one
		var name *string
		if payload.Name != "" {
			name = &payload.Name
		}
		user, err = h.db.CreateUser(payload.Email, "", name, nil)
		if err != nil {
			writeJSON(w, map[string]string{"error": "Failed to create user"}, http.StatusInternalServerError)
			return
		}
		go h.syncUserToOthers(user)
	}

	// Generate JWT token
	token, err := h.generateToken(user)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to generate token"}, http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"token": token}, http.StatusOK)
}

// HandleSyncUser - receives user sync from Woopicx/Shortodella
func (h *Handler) HandleSyncUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify secret from header
	secret := r.Header.Get("X-Service-Secret")
	if h.webhookSecret != "" && subtle.ConstantTimeCompare([]byte(secret), []byte(h.webhookSecret)) != 1 {
		writeJSON(w, map[string]string{"error": "Invalid secret"}, http.StatusUnauthorized)
		return
	}

	var payload SyncUserPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, map[string]string{"error": "Invalid request"}, http.StatusBadRequest)
		return
	}

	if payload.Email == "" {
		writeJSON(w, map[string]string{"error": "Email required"}, http.StatusBadRequest)
		return
	}

	// Create or update user
	syncedFrom := "sync"
	var name *string
	if payload.Name != "" {
		name = &payload.Name
	}

	_, err := h.db.CreateUser(payload.Email, "", name, &syncedFrom)
	if err != nil {
		// User might already exist, that's ok
		fmt.Printf("Sync user %s: %v\n", payload.Email, err)
	}

	writeJSON(w, map[string]bool{"ok": true}, http.StatusOK)
}

// SyncProjectPayload - request from Shortodella to create project
type SyncProjectPayload struct {
	Email  string `json:"email"`
	Domain string `json:"domain"`
	Name   string `json:"name,omitempty"`
}

// SyncProjectResponse - response with created project info
type SyncProjectResponse struct {
	Domain  string `json:"domain"`
	APIKey  string `json:"api_key"`
	Snippet string `json:"snippet"`
}

// HandleSyncProject - receives project sync from Shortodella
// Creates user if needed, creates project, returns API key and snippet
func (h *Handler) HandleSyncProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify secret from header
	secret := r.Header.Get("X-Service-Secret")
	if h.webhookSecret != "" && subtle.ConstantTimeCompare([]byte(secret), []byte(h.webhookSecret)) != 1 {
		writeJSON(w, map[string]string{"error": "Invalid secret"}, http.StatusUnauthorized)
		return
	}

	var payload SyncProjectPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, map[string]string{"error": "Invalid request"}, http.StatusBadRequest)
		return
	}

	if payload.Email == "" || payload.Domain == "" {
		writeJSON(w, map[string]string{"error": "Email and domain required"}, http.StatusBadRequest)
		return
	}

	// Find or create user
	user, err := h.db.GetUserByEmail(payload.Email)
	if err != nil {
		// User doesn't exist, create one
		syncedFrom := "shortodella"
		user, err = h.db.CreateUser(payload.Email, "", nil, &syncedFrom)
		if err != nil {
			writeJSON(w, map[string]string{"error": "Failed to create user"}, http.StatusInternalServerError)
			return
		}
	}

	// Create project
	var name *string
	if payload.Name != "" {
		name = &payload.Name
	}

	project, err := h.db.CreateProject(user.ID, payload.Domain, name)
	if err != nil {
		// Project might already exist
		writeJSON(w, map[string]string{"error": "Failed to create project: " + err.Error()}, http.StatusConflict)
		return
	}

	// Generate snippet
	snippet := fmt.Sprintf(`<script>
!function(t,e){if(!e.cr){var n=t.createElement("script");
n.src="https://shortid.me/cr.js";n.async=1;
t.head.appendChild(n);e.cr=function(){
(e.cr.q=e.cr.q||[]).push(arguments)}}}(document,window);

cr('init', '%s');
</script>`, payload.Domain)

	writeJSON(w, SyncProjectResponse{
		Domain:  project.Domain,
		APIKey:  project.APIKey,
		Snippet: snippet,
	}, http.StatusCreated)
}

// Project handlers
func (h *Handler) HandleGetProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := h.getUserFromRequest(r)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Unauthorized"}, http.StatusUnauthorized)
		return
	}

	projects, err := h.db.GetProjectsByUserID(user.ID)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to get projects"}, http.StatusInternalServerError)
		return
	}

	if projects == nil {
		projects = []Project{}
	}

	writeJSON(w, projects, http.StatusOK)
}

type CreateProjectRequest struct {
	Domain string `json:"domain"`
	Name   string `json:"name,omitempty"`
}

func (h *Handler) HandleCreateProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := h.getUserFromRequest(r)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Unauthorized"}, http.StatusUnauthorized)
		return
	}

	// Demo users cannot create projects
	if user.Role == "demo" {
		writeJSON(w, map[string]string{"error": "Demo mode is read-only"}, http.StatusForbidden)
		return
	}

	var req CreateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "Invalid request"}, http.StatusBadRequest)
		return
	}

	if req.Domain == "" {
		writeJSON(w, map[string]string{"error": "Domain required"}, http.StatusBadRequest)
		return
	}

	var name *string
	if req.Name != "" {
		name = &req.Name
	}

	project, err := h.db.CreateProject(user.ID, req.Domain, name)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to create project"}, http.StatusInternalServerError)
		return
	}

	writeJSON(w, project, http.StatusCreated)
}

func (h *Handler) HandleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := h.getUserFromRequest(r)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Unauthorized"}, http.StatusUnauthorized)
		return
	}

	// Demo users cannot delete projects
	if user.Role == "demo" {
		writeJSON(w, map[string]string{"error": "Demo mode is read-only"}, http.StatusForbidden)
		return
	}

	projectID := r.URL.Query().Get("id")
	if projectID == "" {
		writeJSON(w, map[string]string{"error": "Project ID required"}, http.StatusBadRequest)
		return
	}

	if err := h.db.DeleteProject(projectID, user.ID); err != nil {
		writeJSON(w, map[string]string{"error": "Failed to delete project"}, http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}

// ValidateAPIKey checks if an API key is valid and returns the project
func (h *Handler) ValidateAPIKey(apiKey string) (*Project, error) {
	return h.db.GetProjectByAPIKey(apiKey)
}

// GetUserProjects returns projects for a user (for filtering stats)
func (h *Handler) GetUserDomainsFromToken(r *http.Request) ([]string, error) {
	user, err := h.getUserFromRequest(r)
	if err != nil {
		return nil, err
	}

	projects, err := h.db.GetProjectsByUserID(user.ID)
	if err != nil {
		return nil, err
	}

	domains := make([]string, len(projects))
	for i, p := range projects {
		domains[i] = p.Domain
	}
	return domains, nil
}

// Check if user exists
func (h *Handler) UserExists(email string) bool {
	_, err := h.db.GetUserByEmail(email)
	return err != sql.ErrNoRows
}

func writeJSON(w http.ResponseWriter, data interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// Admin handlers

// isAdmin checks if user has admin role
func (h *Handler) isAdmin(r *http.Request) bool {
	claims, err := h.getClaimsFromRequest(r)
	if err != nil {
		return false
	}
	return claims.Role == "admin"
}

func (h *Handler) getClaimsFromRequest(r *http.Request) (*Claims, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return nil, fmt.Errorf("no authorization header")
	}

	parts := strings.Split(auth, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return nil, fmt.Errorf("invalid authorization header")
	}

	return h.validateToken(parts[1])
}

// HandleAdminProjects returns all projects (admin only)
func (h *Handler) HandleAdminProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.isAdmin(r) {
		writeJSON(w, map[string]string{"error": "Admin access required"}, http.StatusForbidden)
		return
	}

	projects, err := h.db.GetAllProjectsAdmin()
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to get projects"}, http.StatusInternalServerError)
		return
	}

	if projects == nil {
		projects = []ProjectWithUser{}
	}

	writeJSON(w, projects, http.StatusOK)
}

// HandleAdminUsers returns all users (admin only)
func (h *Handler) HandleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.isAdmin(r) {
		writeJSON(w, map[string]string{"error": "Admin access required"}, http.StatusForbidden)
		return
	}

	users, err := h.db.GetAllUsersAdmin()
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to get users"}, http.StatusInternalServerError)
		return
	}

	if users == nil {
		users = []User{}
	}

	writeJSON(w, users, http.StatusOK)
}

// HandleSyncDomains returns all domains for sync between servers
func (h *Handler) HandleSyncDomains(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check sync secret
	secret := r.Header.Get("X-Sync-Secret")
	if h.syncSecret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(h.syncSecret)) != 1 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	domains, err := h.db.GetAllDomains()
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to get domains"}, http.StatusInternalServerError)
		return
	}

	if domains == nil {
		domains = []string{}
	}

	writeJSON(w, map[string]interface{}{"domains": domains}, http.StatusOK)
}

// Funnel handlers

type FunnelStepDef struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Text  string `json:"text,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

type FunnelRequest struct {
	Name   string          `json:"name"`
	Window int             `json:"window"`
	Steps  []FunnelStepDef `json:"steps"`
}

type FunnelResponse struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	Window    int             `json:"window"`
	Steps     []FunnelStepDef `json:"steps"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

func funnelToResponse(f *Funnel) FunnelResponse {
	var steps []FunnelStepDef
	json.Unmarshal([]byte(f.Steps), &steps)
	return FunnelResponse{
		ID:        f.ID,
		ProjectID: f.ProjectID,
		Name:      f.Name,
		Window:    f.Window,
		Steps:     steps,
		CreatedAt: f.CreatedAt,
		UpdatedAt: f.UpdatedAt,
	}
}

// HandleGetFunnels returns all funnels for a project
func (h *Handler) HandleGetFunnels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := h.getUserFromRequest(r)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Unauthorized"}, http.StatusUnauthorized)
		return
	}

	domain := r.URL.Query().Get("domain")
	if domain == "" {
		writeJSON(w, map[string]string{"error": "Domain required"}, http.StatusBadRequest)
		return
	}

	// Verify user owns this domain
	project, err := h.db.GetProjectByDomainAndUserID(domain, user.ID)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Project not found"}, http.StatusNotFound)
		return
	}

	funnels, err := h.db.GetFunnelsByProjectID(project.ID)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to get funnels"}, http.StatusInternalServerError)
		return
	}

	result := make([]FunnelResponse, len(funnels))
	for i, f := range funnels {
		result[i] = funnelToResponse(&f)
	}

	writeJSON(w, result, http.StatusOK)
}

// HandleCreateFunnel creates a new funnel
func (h *Handler) HandleCreateFunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := h.getUserFromRequest(r)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Unauthorized"}, http.StatusUnauthorized)
		return
	}

	// Demo users cannot create funnels
	if user.Role == "demo" {
		writeJSON(w, map[string]string{"error": "Demo mode is read-only"}, http.StatusForbidden)
		return
	}

	domain := r.URL.Query().Get("domain")
	if domain == "" {
		writeJSON(w, map[string]string{"error": "Domain required"}, http.StatusBadRequest)
		return
	}

	// Verify user owns this domain
	project, err := h.db.GetProjectByDomainAndUserID(domain, user.ID)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Project not found"}, http.StatusNotFound)
		return
	}

	var req FunnelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "Invalid request"}, http.StatusBadRequest)
		return
	}

	if req.Name == "" || len(req.Steps) < 2 {
		writeJSON(w, map[string]string{"error": "Name and at least 2 steps required"}, http.StatusBadRequest)
		return
	}

	stepsJSON, _ := json.Marshal(req.Steps)

	funnel, err := h.db.CreateFunnel(project.ID, req.Name, req.Window, string(stepsJSON))
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to create funnel"}, http.StatusInternalServerError)
		return
	}

	writeJSON(w, funnelToResponse(funnel), http.StatusCreated)
}

// HandleUpdateFunnel updates a funnel
func (h *Handler) HandleUpdateFunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := h.getUserFromRequest(r)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Unauthorized"}, http.StatusUnauthorized)
		return
	}

	// Demo users cannot update funnels
	if user.Role == "demo" {
		writeJSON(w, map[string]string{"error": "Demo mode is read-only"}, http.StatusForbidden)
		return
	}

	domain := r.URL.Query().Get("domain")
	funnelID := r.URL.Query().Get("id")
	if domain == "" || funnelID == "" {
		writeJSON(w, map[string]string{"error": "Domain and funnel ID required"}, http.StatusBadRequest)
		return
	}

	// Verify user owns this domain
	project, err := h.db.GetProjectByDomainAndUserID(domain, user.ID)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Project not found"}, http.StatusNotFound)
		return
	}

	var req FunnelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "Invalid request"}, http.StatusBadRequest)
		return
	}

	stepsJSON, _ := json.Marshal(req.Steps)

	funnel, err := h.db.UpdateFunnel(funnelID, project.ID, req.Name, req.Window, string(stepsJSON))
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to update funnel"}, http.StatusInternalServerError)
		return
	}

	writeJSON(w, funnelToResponse(funnel), http.StatusOK)
}

// HandleDeleteFunnel deletes a funnel
func (h *Handler) HandleDeleteFunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := h.getUserFromRequest(r)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Unauthorized"}, http.StatusUnauthorized)
		return
	}

	// Demo users cannot delete funnels
	if user.Role == "demo" {
		writeJSON(w, map[string]string{"error": "Demo mode is read-only"}, http.StatusForbidden)
		return
	}

	domain := r.URL.Query().Get("domain")
	funnelID := r.URL.Query().Get("id")
	if domain == "" || funnelID == "" {
		writeJSON(w, map[string]string{"error": "Domain and funnel ID required"}, http.StatusBadRequest)
		return
	}

	// Verify user owns this domain
	project, err := h.db.GetProjectByDomainAndUserID(domain, user.ID)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Project not found"}, http.StatusNotFound)
		return
	}

	if err := h.db.DeleteFunnel(funnelID, project.ID); err != nil {
		writeJSON(w, map[string]string{"error": "Failed to delete funnel"}, http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}

// HandleDemoLogin returns a token for the demo user (read-only access)
func (h *Handler) HandleDemoLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get demo user by fixed email
	user, err := h.db.GetUserByEmail("demo@shortid.me")
	if err != nil {
		writeJSON(w, map[string]string{"error": "Demo mode not available"}, http.StatusNotFound)
		return
	}

	// Generate token with demo role
	token, err := h.generateToken(user)
	if err != nil {
		writeJSON(w, map[string]string{"error": "Failed to generate token"}, http.StatusInternalServerError)
		return
	}

	writeJSON(w, AuthResponse{Token: token, User: user}, http.StatusOK)
}
