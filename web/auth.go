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
	"unicode"
)

const (
	sessionCookieName = "session_token"
	sessionDuration   = 24 * time.Hour
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
}

// sessionUser is the signed-in identity, echoed to the SPA by /api/session and
// rendered as the account button's monogram.
type sessionUser struct {
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	Monogram string `json:"monogram"`
}

type session struct {
	expires time.Time
	user    sessionUser
}

type AuthManager struct {
	expectedUser string
	expectedPass string
	oidc         *oidcClient
	sessions     map[string]session
	authStates   map[string]oidcState
	mu           sync.RWMutex
}

// NewAuthManager builds the auth manager. The error is non-nil only when OIDC
// discovery fails, so a typo'd issuer URL fails at boot instead of at the first
// sign-in; callers treat it as fatal.
func NewAuthManager(cfg AuthConfig) (*AuthManager, error) {
	a := &AuthManager{
		expectedUser: cfg.User,
		expectedPass: cfg.Pass,
		sessions:     make(map[string]session),
		authStates:   make(map[string]oidcState),
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

func (a *AuthManager) generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// currentSession returns the session for the request cookie, or nil when there
// is none, when the token is unknown, or when it has expired. With no sign-in
// method configured it always returns nil: there is no open mode.
func (a *AuthManager) currentSession(r *http.Request) *session {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	sess, exists := a.sessions[cookie.Value]
	if !exists {
		return nil
	}

	if time.Now().After(sess.expires) {
		delete(a.sessions, cookie.Value)
		return nil
	}

	return &sess
}

func (a *AuthManager) isAuthenticated(r *http.Request) bool {
	return a.currentSession(r) != nil
}

// startSession mints a token for user and sets the session cookie. The cookie is
// HttpOnly, so the client never sees the token.
func (a *AuthManager) startSession(w http.ResponseWriter, user sessionUser) {
	token := a.generateToken()
	expires := time.Now().Add(sessionDuration)

	a.mu.Lock()
	a.sessions[token] = session{expires: expires, user: user}
	a.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
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

	if sess := a.currentSession(r); sess != nil {
		info.Authenticated = true
		info.User = &sess.user
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
