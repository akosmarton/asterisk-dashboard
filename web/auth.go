package web

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"html/template"
	"net/http"
	"sync"
	"time"
)

//go:embed login.html
var loginHTML string

const (
	sessionCookieName = "session_token"
	sessionDuration   = 24 * time.Hour
)

type AuthManager struct {
	expectedUser string
	expectedPass string
	loginTmpl    *template.Template
	sessions     map[string]time.Time
	mu           sync.RWMutex
}

func NewAuthManager(user, pass string) *AuthManager {
	tmpl := template.Must(template.New("login").Parse(loginHTML))
	return &AuthManager{
		expectedUser: user,
		expectedPass: pass,
		loginTmpl:    tmpl,
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

func (a *AuthManager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if a.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = a.loginTmpl.Execute(w, nil)
		return
	}

	if r.Method == http.MethodPost {
		username := r.FormValue("username")
		password := r.FormValue("password")

		userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(a.expectedUser)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(password), []byte(a.expectedPass)) == 1

		if !userMatch || !passMatch {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = a.loginTmpl.Execute(w, map[string]string{
				"Error": "Invalid username or password.",
			})
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

		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func (a *AuthManager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && cookie.Value != "" {
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

	http.Redirect(w, r, "/login", http.StatusFound)
}

func (a *AuthManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" || r.URL.Path == "/favicon.svg" || r.URL.Path == "/favicon.ico" {
			next.ServeHTTP(w, r)
			return
		}

		if !a.isAuthenticated(r) {
			if r.Header.Get("Upgrade") == "websocket" || r.URL.Path == "/api/data" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		next.ServeHTTP(w, r)
	})
}
