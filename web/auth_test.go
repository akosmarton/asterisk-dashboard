package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthManager(t *testing.T) {
	mux := http.NewServeMux()
	authMgr, err := NewAuthManager(AuthConfig{User: "admin", Pass: "secret"})
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	mux.HandleFunc("/api/session", authMgr.HandleSession)
	mux.HandleFunc("/api/login", authMgr.HandleLogin)
	mux.HandleFunc("/api/logout", authMgr.HandleLogout)
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	// The SPA shell stands in for web.Server.HandleIndex.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("spa shell"))
	})

	ts := httptest.NewServer(authMgr.Middleware(mux))
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	postJSON := func(path, body string) *http.Response {
		t.Helper()
		resp, err := client.Post(ts.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST %s failed: %v", path, err)
		}
		return resp
	}

	get := func(path string) *http.Response {
		t.Helper()
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		return resp
	}

	// sessionInfoOf reads /api/session and returns the decoded response.
	sessionInfoOf := func() sessionInfo {
		t.Helper()
		resp := get("/api/session")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/session: expected 200, got %d", resp.StatusCode)
		}
		var body sessionInfo
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("GET /api/session: bad JSON: %v", err)
		}
		return body
	}

	// sessionAuthenticated reads /api/session and returns the flag.
	sessionAuthenticated := func() bool {
		t.Helper()
		return sessionInfoOf().Authenticated
	}

	// errorMessage reads a JSON error body.
	errorMessage := func(resp *http.Response) string {
		t.Helper()
		defer resp.Body.Close()
		var body struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("expected a JSON error body, got %v", err)
		}
		return body.Error
	}

	// 1. The SPA shell is public, even without a session.
	resp := get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for the shell, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. No session yet — but the enabled sign-in methods are advertised.
	info := sessionInfoOf()
	if info.Authenticated {
		t.Fatal("expected an unauthenticated session before login")
	}
	if !info.PasswordLogin || info.OIDCLogin {
		t.Fatalf("expected password_login only, got %+v", info)
	}
	if info.User != nil {
		t.Fatalf("expected no user before login, got %+v", info.User)
	}

	// 3. Data endpoints are refused without a session.
	resp = get("/api/data")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for /api/data, got %d", resp.StatusCode)
	}
	if msg := errorMessage(resp); msg == "" {
		t.Fatal("expected an error message for the refused request")
	}

	// 4. A malformed body is a bad request, not a failed login.
	resp = postJSON("/api/login", "{not json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed body, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 5. Wrong credentials.
	resp = postJSON("/api/login", `{"username":"admin","password":"wrongpass"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad credentials, got %d", resp.StatusCode)
	}
	if msg := errorMessage(resp); msg == "" {
		t.Fatal("expected an error message for bad credentials")
	}
	if sessionAuthenticated() {
		t.Fatal("session must not be authenticated after a failed login")
	}

	// 6. Correct credentials set an HttpOnly session cookie. The jar drops the
	//    HttpOnly flag, so read it from the response header.
	resp = postJSON("/api/login", `{"username":"admin","password":"secret"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for a successful login, got %d", resp.StatusCode)
	}
	setCookie := resp.Header.Get("Set-Cookie")
	var loginBody struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginBody); err != nil || !loginBody.OK {
		t.Fatalf("expected {ok:true}, got %v (err %v)", loginBody, err)
	}
	resp.Body.Close()

	session, err := http.ParseSetCookie(setCookie)
	if err != nil {
		t.Fatalf("expected a session cookie, got %q (%v)", setCookie, err)
	}
	if session.Name != sessionCookieName {
		t.Fatalf("expected cookie %q, got %q", sessionCookieName, session.Name)
	}
	if !session.HttpOnly {
		t.Error("the session cookie must be HttpOnly")
	}
	if session.Path != "/" {
		t.Errorf("expected cookie path /, got %q", session.Path)
	}

	// 7. The session is now valid, and carries the signed-in identity.
	if !sessionAuthenticated() {
		t.Fatal("expected an authenticated session after login")
	}
	info = sessionInfoOf()
	if info.User == nil || info.User.Name != "admin" || info.User.Monogram != "A" {
		t.Fatalf("expected the admin identity with monogram A, got %+v", info.User)
	}

	// 8. Data is served with the session.
	resp = get("/api/data")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /api/data with a session, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 9. Signing out drops the session.
	resp = postJSON("/api/logout", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for logout, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 10. …and the session is gone again.
	if sessionAuthenticated() {
		t.Fatal("expected an unauthenticated session after logout")
	}
	resp = get("/api/data")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 11. The login endpoint is POST-only.
	resp = get("/api/login")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /api/login, got %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// TestAuthManagerWithoutSignInMethod pins the locked state: with no password
// pair and no OIDC block there is no open mode — nothing authenticates.
func TestAuthManagerWithoutSignInMethod(t *testing.T) {
	authMgr, err := NewAuthManager(AuthConfig{})
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/session", authMgr.HandleSession)
	mux.HandleFunc("/api/login", authMgr.HandleLogin)
	mux.HandleFunc("/api/oidc/login", authMgr.HandleOIDCLogin)
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	ts := httptest.NewServer(authMgr.Middleware(mux))
	defer ts.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// An empty credential pair must not match an empty configuration.
	resp, err := client.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"username":"","password":""}`))
	if err != nil {
		t.Fatalf("POST /api/login failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an empty credential pair, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = client.Get(ts.URL + "/api/data")
	if err != nil {
		t.Fatalf("GET /api/data failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for /api/data, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// SSO is off too, so the entry point bounces back to the login view.
	resp, err = client.Get(ts.URL + "/api/oidc/login")
	if err != nil {
		t.Fatalf("GET /api/oidc/login failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 for /api/oidc/login, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/?auth_error=failed" {
		t.Fatalf("expected the login view, got %q", loc)
	}
}

func TestMonogramOf(t *testing.T) {
	cases := []struct {
		name, email, want string
	}{
		{"Jane Doe", "jane.doe@example.com", "JD"},
		{"", "jane.doe@example.com", "JD"},
		{"admin", "", "A"},
		{"", "", "?"},
		{"0123", "", "0"},
	}
	for _, c := range cases {
		if got := monogramOf(c.name, c.email); got != c.want {
			t.Errorf("monogramOf(%q, %q) = %q, want %q", c.name, c.email, got, c.want)
		}
	}
}
