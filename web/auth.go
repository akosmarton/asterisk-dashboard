package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

const (
	// maxLoginBody caps the JSON credentials body.
	maxLoginBody = 4096
)

// AuthConfig describes the enabled sign-in methods. Password sign-in is on when
// User is set, SSO is on when OIDC is non-nil; with neither, nothing
// authenticates. main validates the combinations at startup.
type AuthConfig struct {
	User string // ADMIN_USER; empty = password sign-in off
	Pass string
	OIDC *OIDCConfig // nil = SSO off
	// SessionSecret is the SESSION_SECRET the session cookies are signed with.
	// It is required (at least 32 bytes) whenever a sign-in method is enabled;
	// with neither method configured it is unused.
	SessionSecret string
}

// sessionUser is the signed-in identity, echoed to the SPA by /api/session and
// rendered as the account button's monogram.
type sessionUser struct {
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	Monogram string `json:"monogram"`
}

type AuthManager struct {
	expectedUser string
	expectedPass string
	oidc         *oidcClient
	sessions     *sessionCodec // signs and verifies the session cookie
	loginLimiter *loginLimiter
	authStates   map[string]oidcState
	mu           sync.RWMutex
}

// NewAuthManager builds the auth manager. Errors are fatal to the caller: an
// unusable SESSION_SECRET or a failed OIDC discovery must not turn into a
// running server whose sign-in silently misbehaves.
func NewAuthManager(cfg AuthConfig) (*AuthManager, error) {
	a := &AuthManager{
		expectedUser: cfg.User,
		expectedPass: cfg.Pass,
		loginLimiter: newLoginLimiter(loginAttempts, loginWindow),
		authStates:   make(map[string]oidcState),
	}

	// Sessions exist only when a sign-in method does. A configured sign-in
	// method without a usable SESSION_SECRET is a startup error: cookies could be
	// minted but never verified, which would look like a broken login rather than
	// a missing variable.
	if cfg.User != "" || cfg.OIDC != nil || cfg.SessionSecret != "" {
		sessions, err := newSessionCodec(cfg.SessionSecret, sessionAudience)
		if err != nil {
			return nil, err
		}
		a.sessions = sessions
	}

	if cfg.OIDC != nil {
		client, err := newOIDCClient(cfg.OIDC)
		if err != nil {
			return nil, err
		}
		a.oidc = client
	}

	return a, nil
}

// currentSession returns the identity carried by the request's session cookie,
// or nil when there is none, when the cookie was forged, or when it has
// expired. With no sign-in method configured it always returns nil: there is
// no open mode.
func (a *AuthManager) currentSession(r *http.Request) *sessionUser {
	if a.sessions == nil {
		return nil
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}
	claims, ok := a.sessions.Decode(cookie.Value)
	if !ok {
		return nil
	}
	return &sessionUser{
		Name:     claims.Name,
		Email:    claims.Email,
		Monogram: monogramOf(claims.Name, claims.Email),
	}
}

func (a *AuthManager) isAuthenticated(r *http.Request) bool {
	return a.currentSession(r) != nil
}

// startSession sets the session cookie for user. The cookie carries the signed
// identity, so the same key after a restart keeps the sign-in valid; it is
// HttpOnly, so the client never sees the token.
func (a *AuthManager) startSession(w http.ResponseWriter, r *http.Request, user sessionUser) error {
	if a.sessions == nil {
		return fmt.Errorf("session signing is not configured")
	}
	subject := user.Email
	if subject == "" {
		subject = user.Name
	}
	token, expires, err := a.sessions.Issue(subject, user.Name, user.Email)
	if err != nil {
		return err
	}
	http.SetCookie(w, newSessionCookie(sessionCookieName, sessionCookiePath, token, expires, secureRequest(r)))
	return nil
}

// secureRequest reports whether the browser reached the server over HTTPS,
// either directly or through a proxy that forwarded the original scheme.
func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	for _, proto := range strings.Split(r.Header.Get("X-Forwarded-Proto"), ",") {
		if strings.EqualFold(strings.TrimSpace(proto), "https") {
			return true
		}
	}
	return false
}

// constantTimeEqual compares two secrets without leaking their contents or
// lengths through timing. Hashing to fixed 32-byte digests is what makes the
// comparison cost flat even when the inputs differ in length.
func constantTimeEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// monogramOf returns up to two uppercase initials for the account button: from
// the display name, or from the local part of the email when the name is empty.
// "Jane Doe" -> "JD", "jane.doe@example.com" -> "JD", "admin" -> "A".
func monogramOf(name, email string) string {
	source := strings.TrimSpace(name)
	if source == "" {
		source, _, _ = strings.Cut(email, "@")
	}

	fields := strings.FieldsFunc(source, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	var initials []rune
	for _, field := range fields {
		if len(initials) == 2 {
			break
		}
		initials = append(initials, unicode.ToUpper([]rune(field)[0]))
	}

	if len(initials) == 0 {
		return "?"
	}
	return string(initials)
}

// sessionInfo is the /api/session response: it tells the SPA which view to
// render and which sign-in methods to offer.
type sessionInfo struct {
	Authenticated bool         `json:"authenticated"`
	PasswordLogin bool         `json:"password_login"`
	OIDCLogin     bool         `json:"oidc_login"`
	User          *sessionUser `json:"user"`
}

// HandleSession tells the SPA whether it should render the dashboard or the
// login view. It is public and never fails.
func (a *AuthManager) HandleSession(w http.ResponseWriter, r *http.Request) {
	info := sessionInfo{
		PasswordLogin: a.expectedUser != "",
		OIDCLogin:     a.oidc != nil,
	}

	if user := a.currentSession(r); user != nil {
		info.Authenticated = true
		info.User = user
	}

	writeJSON(w, http.StatusOK, info)
}

// HandleLogin takes JSON credentials and sets the session cookie.
func (a *AuthManager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	// Password sign-in off: never fall through to the constant-time compares,
	// where an empty body would match an empty configuration.
	if a.expectedUser == "" {
		writeJSONError(w, http.StatusUnauthorized, "Invalid username or password.")
		return
	}

	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	if ok, retry := a.loginLimiter.allow(clientIP(r)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeJSONError(w, http.StatusTooManyRequests, "Too Many Requests.")
		return
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&creds); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "Request Entity Too Large.")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "Malformed request.")
		return
	}

	// Both sides are hashed first: ConstantTimeCompare returns immediately when
	// the lengths differ, which would leak the configured credential lengths.
	userMatch := constantTimeEqual(creds.Username, a.expectedUser)
	passMatch := constantTimeEqual(creds.Password, a.expectedPass)

	if !userMatch || !passMatch {
		writeJSONError(w, http.StatusUnauthorized, "Invalid username or password.")
		return
	}

	if err := a.startSession(w, r, sessionUser{Name: a.expectedUser, Monogram: monogramOf(a.expectedUser, "")}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// HandleLogout clears the session cookie. It is idempotent: signing out without
// a session is not an error. The cookie is self-contained, so this signs out the
// browser holding it; a copy taken earlier stays valid until its expiry, like
// any bearer token.
func (a *AuthManager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	// Public and idempotent: an expired cookie must be clearable, but a
	// cross-site request (which SameSite=Lax sends without the cookie) must not
	// trigger a deletion.
	if _, err := r.Cookie(sessionCookieName); err == nil {
		http.SetCookie(w, clearSessionCookie(sessionCookieName, sessionCookiePath, secureRequest(r)))
	}

	w.WriteHeader(http.StatusNoContent)
}

// publicPath reports whether a request path is served without a session: the
// sign-in endpoints and the frontend build.
func publicPath(p string) bool {
	return p == "/api/login" ||
		p == "/api/session" ||
		p == "/api/oidc/login" ||
		p == "/api/oidc/callback" ||
		p == "/favicon.svg" ||
		p == "/favicon.ico" ||
		strings.HasPrefix(p, "/static/")
}

// dataPath reports whether a path needs a session: the data endpoints. Every
// other path is a page request, and the SPA shell decides what to render (it
// asks /api/session), so page requests are never redirected.
func dataPath(p string) bool {
	return strings.HasPrefix(p, "/api/")
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
