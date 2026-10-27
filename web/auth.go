package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	sessionCookieName    = "session_token"
	sessionCookieVersion = "v1"
	sessionDuration      = 24 * time.Hour
	sessionKeySize       = 32
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
	// The same value after a restart keeps existing sign-ins valid; empty means
	// a random per-process key, so every restart signs everyone out.
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
	sessionKey   []byte // HMAC key for the session cookie; see sessionKeyFromSecret
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
		authStates:   make(map[string]oidcState),
	}

	key, err := sessionKeyFromSecret(cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	a.sessionKey = key

	if cfg.OIDC != nil {
		client, err := newOIDCClient(cfg.OIDC)
		if err != nil {
			return nil, err
		}
		a.oidc = client
	}

	return a, nil
}

// sessionKeyFromSecret turns SESSION_SECRET into the HMAC key that signs the
// session cookies. A stable secret is what keeps a sign-in valid across a
// restart: any other value invalidates every cookie already handed out. An
// empty secret falls back to a random per-process key, which keeps the server
// usable but signs everyone out on restart, so it is logged rather than
// treated as fatal.
func sessionKeyFromSecret(secret string) ([]byte, error) {
	if secret == "" {
		key := make([]byte, sessionKeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate session key: %w", err)
		}
		log.Println("[Auth] SESSION_SECRET is not set: session cookies are signed with a random key, so a restart signs everyone out")
		return key, nil
	}
	if len(secret) < sessionKeySize {
		return nil, fmt.Errorf("SESSION_SECRET must be at least %d characters (generate one with `openssl rand -hex 32`)", sessionKeySize)
	}
	return []byte(secret), nil
}

// sessionClaims is the signed cookie payload: the signed-in identity plus the
// expiry the client cannot move without breaking the HMAC.
type sessionClaims struct {
	User    sessionUser `json:"user"`
	Expires int64       `json:"exp"`
}

// signSession encodes user with its expiry and tags it with the manager's key.
// The MAC covers the payload bytes, so the claims cannot be edited into a
// different identity or a later expiry.
func (a *AuthManager) signSession(user sessionUser, expires time.Time) string {
	payload, err := json.Marshal(sessionClaims{User: user, Expires: expires.Unix()})
	if err != nil {
		panic("web: marshal session claims: " + err.Error()) // strings and an int only
	}
	mac := hmac.New(sha256.New, a.sessionKey)
	mac.Write(payload)
	return sessionCookieVersion + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// parseSession verifies a cookie value and returns the identity it carries.
// The tag is compared in constant time (hmac.Equal), so a forged cookie cannot
// be discovered tag byte by tag byte.
func (a *AuthManager) parseSession(value string) *sessionUser {
	version, rest, ok := strings.Cut(value, ".")
	if !ok || version != sessionCookieVersion {
		return nil
	}
	encodedPayload, encodedMAC, ok := strings.Cut(rest, ".")
	if !ok {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return nil
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(encodedMAC)
	if err != nil {
		return nil
	}
	mac := hmac.New(sha256.New, a.sessionKey)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), gotMAC) {
		return nil
	}

	var claims sessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	if time.Now().After(time.Unix(claims.Expires, 0)) {
		return nil
	}
	return &claims.User
}

// currentSession returns the identity carried by the request's session cookie,
// or nil when there is none, when the cookie was forged, or when it has
// expired. With no sign-in method configured it always returns nil: there is
// no open mode.
func (a *AuthManager) currentSession(r *http.Request) *sessionUser {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}
	return a.parseSession(cookie.Value)
}

func (a *AuthManager) isAuthenticated(r *http.Request) bool {
	return a.currentSession(r) != nil
}

// startSession sets the session cookie for user. The cookie carries the signed
// identity, so the same key after a restart keeps the sign-in valid; it is
// HttpOnly, so the client never sees the token.
func (a *AuthManager) startSession(w http.ResponseWriter, user sessionUser) {
	expires := time.Now().Add(sessionDuration)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    a.signSession(user, expires),
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
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

	a.startSession(w, sessionUser{Name: a.expectedUser, Monogram: monogramOf(a.expectedUser, "")})

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// HandleLogout clears the session cookie. It is idempotent: signing out without
// a session is not an error. The cookie is self-contained, so this signs out the
// browser holding it; a copy taken earlier stays valid until its expiry, like
// any bearer token.
func (a *AuthManager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
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
// sign-in endpoints and the frontend build.
func publicPath(p string) bool {
	return p == "/api/login" ||
		p == "/api/logout" ||
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
