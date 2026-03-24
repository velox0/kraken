package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"kraken/internal/db"
)

type contextKey string

const userContextKey = contextKey("authContext")

// MaxFailedAttempts before account freeze.
const MaxFailedAttempts = 5

type AuthContext struct {
	UserID    int64
	Scopes    []string
	RoleLevel int
}

// randomDelay adds 200-800ms jitter to prevent timing attacks.
func randomDelay() {
	n, _ := rand.Int(rand.Reader, big.NewInt(600))
	delay := time.Duration(200+n.Int64()) * time.Millisecond
	time.Sleep(delay)
}

func adminContactEmail() string {
	e := os.Getenv("ADMIN_CONTACT_EMAIL")
	if e == "" {
		return "your system administrator"
	}
	return e
}

// ---------- Middleware ----------

func (h *Handler) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""

		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			cookie, err := r.Cookie("kraken_session")
			if err == nil {
				token = cookie.Value
			}
		}

		if token == "" {
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized: missing token"))
			return
		}

		hash := sha256.Sum256([]byte(token))
		hashStr := hex.EncodeToString(hash[:])

		apiKey, err := h.store.GetAPIKeyByHash(r.Context(), hashStr)
		if err != nil || apiKey == nil {
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized: invalid token"))
			return
		}

		// Look up the user to get role_level
		var roleLevel int
		if apiKey.UserID != nil {
			user, err := h.store.GetUserByID(r.Context(), *apiKey.UserID)
			if err != nil || user == nil {
				writeError(w, http.StatusUnauthorized, errors.New("unauthorized: user not found"))
				return
			}
			if user.IsFrozen {
				writeError(w, http.StatusForbidden, errors.New("account is frozen"))
				return
			}
			roleLevel = user.RoleLevel
		}

		authCtx := AuthContext{
			Scopes:    apiKey.Scopes,
			RoleLevel: roleLevel,
		}
		if apiKey.UserID != nil {
			authCtx.UserID = *apiKey.UserID
		}

		ctx := context.WithValue(r.Context(), userContextKey, authCtx)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireScope(requiredScope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authCtx, ok := r.Context().Value(userContextKey).(AuthContext)
			if !ok {
				writeError(w, http.StatusUnauthorized, errors.New("unauthorized: context missing"))
				return
			}

			hasScope := false
			for _, s := range authCtx.Scopes {
				if s == "admin" || s == requiredScope {
					hasScope = true
					break
				}
			}

			if !hasScope {
				writeError(w, http.StatusForbidden, errors.New("forbidden: requires scope "+requiredScope))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ---------- Login ----------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		randomDelay()
		writeError(w, http.StatusBadRequest, err)
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	user, err := h.store.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		randomDelay()
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if user == nil {
		randomDelay()
		writeError(w, http.StatusUnauthorized, errors.New("invalid credentials"))
		return
	}

	// Check frozen
	if user.IsFrozen {
		randomDelay()
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":         "Account is frozen. Contact " + adminContactEmail(),
			"frozen":        true,
			"contact_email": adminContactEmail(),
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		// Wrong password — increment failed attempts
		count, _ := h.store.IncrementFailedAttempts(r.Context(), user.ID)
		if count >= MaxFailedAttempts {
			_ = h.store.FreezeUser(r.Context(), user.ID)
			randomDelay()
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":         "Account frozen after too many failed attempts. Contact " + adminContactEmail(),
				"frozen":        true,
				"contact_email": adminContactEmail(),
			})
			return
		}
		randomDelay()
		writeError(w, http.StatusUnauthorized, errors.New("invalid credentials"))
		return
	}

	// Success — reset failed attempts
	_ = h.store.ResetFailedAttempts(r.Context(), user.ID)

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	token := hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	hashStr := hex.EncodeToString(hash[:])

	_, err = h.store.CreateAPIKey(r.Context(), "Session: "+user.Email, hashStr, user.Scopes, &user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "kraken_session",
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(24 * time.Hour * 30),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	randomDelay()
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  user,
	})
}

// ---------- Logout ----------

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	// Delete the session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "kraken_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	// Try to delete session API key from DB
	token := ""
	cookie, err := r.Cookie("kraken_session")
	if err == nil {
		token = cookie.Value
	} else {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if token != "" {
		hash := sha256.Sum256([]byte(token))
		hashStr := hex.EncodeToString(hash[:])
		key, _ := h.store.GetAPIKeyByHash(r.Context(), hashStr)
		if key != nil {
			// Delete this specific key via raw query
			h.store.DeleteAPIKey(r.Context(), key.ID)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// ---------- Auth Me ----------

func (h *Handler) authMe(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := r.Context().Value(userContextKey).(AuthContext)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	user, err := h.store.GetUserByID(r.Context(), authCtx.UserID)
	if err != nil || user == nil {
		writeError(w, http.StatusUnauthorized, errors.New("user not found"))
		return
	}

	writeJSON(w, http.StatusOK, user)
}

// ---------- Password Change ----------

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := r.Context().Value(userContextKey).(AuthContext)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if strings.TrimSpace(req.NewPassword) == "" {
		writeError(w, http.StatusBadRequest, errors.New("new_password is required"))
		return
	}
	if len(req.NewPassword) < 6 {
		writeError(w, http.StatusBadRequest, errors.New("new_password must be at least 6 characters"))
		return
	}

	user, err := h.store.GetUserByID(r.Context(), authCtx.UserID)
	if err != nil || user == nil {
		writeError(w, http.StatusUnauthorized, errors.New("user not found"))
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		writeError(w, http.StatusForbidden, errors.New("current password is incorrect"))
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if err := h.store.UpdateUserPassword(r.Context(), user.ID, string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "password changed"})
}

// ---------- API Key Management ----------

type createAPIKeyRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}

	authCtx, ok := r.Context().Value(userContextKey).(AuthContext)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	token := hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	hashStr := hex.EncodeToString(hash[:])

	uid := authCtx.UserID
	apiKey, err := h.store.CreateAPIKey(r.Context(), req.Name, hashStr, req.Scopes, &uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":   token,
		"api_key": apiKey,
	})
}

// ---------- Admin: User Management ----------

func getAuthCtx(r *http.Request) (AuthContext, bool) {
	authCtx, ok := r.Context().Value(userContextKey).(AuthContext)
	return authCtx, ok
}

func hasScope(scopes []string, scope string) bool {
	for _, s := range scopes {
		if s == "admin" || s == scope {
			return true
		}
	}
	return false
}

// canManageUser checks that the caller has a strictly lower (i.e. more privileged) role_level.
func canManageUser(caller AuthContext, targetRoleLevel int) bool {
	return caller.RoleLevel < targetRoleLevel
}

// filterAssignableScopes ensures the caller can only assign scopes they themselves hold.
func filterAssignableScopes(callerScopes, requested []string) []string {
	callerSet := map[string]bool{}
	isAdmin := false
	for _, s := range callerScopes {
		callerSet[s] = true
		if s == "admin" {
			isAdmin = true
		}
	}
	if isAdmin {
		return requested
	}
	var result []string
	for _, s := range requested {
		if callerSet[s] {
			result = append(result, s)
		}
	}
	if result == nil {
		result = []string{}
	}
	return result
}

func (h *Handler) adminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

type createUserRequest struct {
	Email       string   `json:"email"`
	Password    string   `json:"password"`
	DisplayName string   `json:"display_name"`
	Scopes      []string `json:"scopes"`
	RoleLevel   int      `json:"role_level"`
}

func (h *Handler) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := getAuthCtx(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || strings.TrimSpace(req.Password) == "" {
		writeError(w, http.StatusBadRequest, errors.New("email and password are required"))
		return
	}
	if len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, errors.New("password must be at least 6 characters"))
		return
	}

	// Ensure new user has lower privilege (higher number)
	if req.RoleLevel <= caller.RoleLevel {
		writeError(w, http.StatusForbidden, errors.New("cannot create a user with equal or higher privilege than your own"))
		return
	}

	req.Scopes = filterAssignableScopes(caller.Scopes, req.Scopes)

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	user, err := h.store.CreateUser(r.Context(), req.Email, string(hash), req.DisplayName, req.Scopes, req.RoleLevel)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			writeError(w, http.StatusConflict, errors.New("a user with this email already exists"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, user)
}

func (h *Handler) adminGetUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	user, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, errors.New("user not found"))
		return
	}

	writeJSON(w, http.StatusOK, user)
}

type updateUserRequest struct {
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Scopes      []string `json:"scopes"`
	RoleLevel   int      `json:"role_level"`
	Password    string   `json:"password,omitempty"` // Optional — if set, reset password
}

func (h *Handler) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := getAuthCtx(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	target, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, errors.New("user not found"))
		return
	}

	if !canManageUser(caller, target.RoleLevel) {
		writeError(w, http.StatusForbidden, errors.New("cannot modify a user with equal or higher privilege"))
		return
	}

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, errors.New("email is required"))
		return
	}

	// Don't allow escalating to same or higher privilege as caller
	if req.RoleLevel <= caller.RoleLevel {
		writeError(w, http.StatusForbidden, errors.New("cannot assign equal or higher privilege than your own"))
		return
	}

	req.Scopes = filterAssignableScopes(caller.Scopes, req.Scopes)

	updated, err := h.store.UpdateUser(r.Context(), userID, db.UpdateUserParams{
		Email:       req.Email,
		DisplayName: req.DisplayName,
		Scopes:      req.Scopes,
		RoleLevel:   req.RoleLevel,
	})
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			writeError(w, http.StatusConflict, errors.New("a user with this email already exists"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Optionally reset password
	if strings.TrimSpace(req.Password) != "" {
		if len(req.Password) < 6 {
			writeError(w, http.StatusBadRequest, errors.New("password must be at least 6 characters"))
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if err := h.store.UpdateUserPassword(r.Context(), userID, string(hash)); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := getAuthCtx(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	target, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, errors.New("user not found"))
		return
	}

	if !canManageUser(caller, target.RoleLevel) {
		writeError(w, http.StatusForbidden, errors.New("cannot delete a user with equal or higher privilege"))
		return
	}

	// Delete user's sessions first
	_ = h.store.DeleteAPIKeysByUser(r.Context(), userID)
	if err := h.store.DeleteUser(r.Context(), userID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "user_id": userID})
}

func (h *Handler) adminUnfreezeUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := getAuthCtx(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	target, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, errors.New("user not found"))
		return
	}

	if !canManageUser(caller, target.RoleLevel) {
		writeError(w, http.StatusForbidden, errors.New("cannot unfreeze a user with equal or higher privilege"))
		return
	}

	if err := h.store.UnfreezeUser(r.Context(), userID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "unfrozen"})
}
