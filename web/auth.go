package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "session_token"
	sessionDuration   = 24 * time.Hour
	// maxLoginBody caps the JSON credentials body.
	maxLoginBody = 4096
)

type AuthManager struct {
	expectedUser string
	expectedPass string
	sessions     map[string]time.Time
	mu           sync.RWMutex
}

func NewAuthManager(user, pass string) *AuthManager {
	return &AuthManager{
		expectedUser: user,
		expectedPass: pass,
		sessions:     make(map[string]time.Time),
	}
}

func (a *AuthManager) generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *AuthManager) isAuthenticated(r *http.Request) bool {
	if a.expectedUser == "" && a.expectedPass == "" {
		return true
	}

	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	expires, exists := a.sessions[cookie.Value]
	if !exists {
		return false
	}

	if time.Now().After(expires) {
		delete(a.sessions, cookie.Value)
		return false
	}

	return true
}

// HandleSession tells the SPA whether it should render the dashboard or the
// login view. It is public and never fails.
func (a *AuthManager) HandleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": a.isAuthenticated(r)})
}

// HandleLogin takes JSON credentials and sets the session cookie. The cookie is
// HttpOnly, so the client never sees the token.
func (a *AuthManager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&creds); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Malformed request.")
		return
	}

	userMatch := subtle.ConstantTimeCompare([]byte(creds.Username), []byte(a.expectedUser)) == 1
	passMatch := subtle.ConstantTimeCompare([]byte(creds.Password), []byte(a.expectedPass)) == 1

	if !userMatch || !passMatch {
		writeJSONError(w, http.StatusUnauthorized, "Invalid username or password.")
		return
	}

	token := a.generateToken()
	expires := time.Now().Add(sessionDuration)

	a.mu.Lock()
	a.sessions[token] = expires
	a.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// HandleLogout drops the session and the cookie. It is idempotent: signing out
// without a session is not an error.
func (a *AuthManager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusNoContent)
}

// publicPath reports whether a request path is served without a session: the
// login endpoints and the frontend build.
func publicPath(p string) bool {
	return p == "/api/login" ||
		p == "/api/logout" ||
		p == "/api/session" ||
		p == "/favicon.svg" ||
		p == "/favicon.ico" ||
		strings.HasPrefix(p, "/static/")
}

// dataPath reports whether a path needs a session: the data endpoints. Every
// other path is a page request, and the SPA shell decides what to render (it
// asks /api/session), so page requests are never redirected.
func dataPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || p == "/ws"
}

func (a *AuthManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !dataPath(r.URL.Path) || publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		if !a.isAuthenticated(r) {
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		next.ServeHTTP(w, r)
	})
}
