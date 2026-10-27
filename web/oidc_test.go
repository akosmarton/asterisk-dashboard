package web

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIDP is a minimal OpenID Provider: discovery, JWKS, an authorization
// endpoint that records the PKCE challenge and a token endpoint that checks the
// verifier against it — so a successful flow proves both PKCE legs.
type fakeIDP struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	// claims put into the ID token
	name          string
	email         string
	emailVerified *bool

	// rejectExchange makes the token endpoint refuse the exchange, the way a
	// provider that cannot validate the PKCE proof would.
	rejectExchange bool

	// wantRedirectHost, when set, is the host the redirect_uri must point at.
	wantRedirectHost string

	mu         sync.Mutex
	challenge  map[string]string // state -> code_challenge
	redirectTo string            // redirect_uri seen at /auth
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the test key: %v", err)
	}

	idp := &fakeIDP{
		t:         t,
		key:       key,
		name:      "Jane Doe",
		email:     "jane.doe@example.com",
		challenge: make(map[string]string),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSONDoc(t, w, map[string]any{
			"issuer":                                idp.srv.URL,
			"authorization_endpoint":                idp.srv.URL + "/auth",
			"token_endpoint":                        idp.srv.URL + "/token",
			"jwks_uri":                              idp.srv.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		writeJSONDoc(t, w, map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": "test-key",
				"alg": "RS256",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(idp.key.N.Bytes()),
				"e":   "AQAB",
			}},
		})
	})

	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		if got := q.Get("response_type"); got != "code" {
			t.Errorf("/auth: expected response_type=code, got %q", got)
		}
		if got := q.Get("client_id"); got != "dashboard" {
			t.Errorf("/auth: expected client_id=dashboard, got %q", got)
		}
		if got := q.Get("code_challenge_method"); got != "S256" {
			t.Errorf("/auth: expected code_challenge_method=S256, got %q", got)
		}
		if !strings.Contains(q.Get("scope"), "openid") {
			t.Errorf("/auth: expected an openid scope, got %q", q.Get("scope"))
		}

		state := q.Get("state")
		challenge := q.Get("code_challenge")
		redirectURI := q.Get("redirect_uri")
		if state == "" || challenge == "" || redirectURI == "" {
			t.Errorf("/auth: missing state (%q), code_challenge (%q) or redirect_uri (%q)", state, challenge, redirectURI)
			http.Error(w, "bad authorization request", http.StatusBadRequest)
			return
		}

		if u, err := url.Parse(redirectURI); err != nil {
			t.Errorf("/auth: unparsable redirect_uri %q: %v", redirectURI, err)
		} else if !strings.HasSuffix(u.Path, "/api/oidc/callback") {
			t.Errorf("/auth: expected a redirect_uri ending in /api/oidc/callback, got %q", redirectURI)
		} else if idp.wantRedirectHost != "" && u.Host != idp.wantRedirectHost {
			t.Errorf("/auth: expected redirect_uri host %q, got %q", idp.wantRedirectHost, u.Host)
		}

		idp.mu.Lock()
		if !idp.rejectExchange {
			idp.challenge[state] = challenge
		}
		idp.redirectTo = redirectURI
		idp.mu.Unlock()

		http.Redirect(w, r, redirectURI+"?code=test-code&state="+url.QueryEscape(state), http.StatusFound)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("/token: unparsable form: %v", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}

		if got := r.Form.Get("code"); got != "test-code" {
			t.Errorf("/token: expected code=test-code, got %q", got)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Errorf("/token: expected grant_type=authorization_code, got %q", got)
		}

		idp.mu.Lock()
		redirectTo := idp.redirectTo
		verifierOK := false
		for _, challenge := range idp.challenge {
			if challengeFromVerifier(r.Form.Get("code_verifier")) == challenge {
				verifierOK = true
				break
			}
		}
		idp.mu.Unlock()

		// The exchange leg must repeat the redirect_uri of the authorization
		// leg: the provider compares them.
		if redirectTo != "" && r.Form.Get("redirect_uri") != redirectTo {
			t.Errorf("/token: redirect_uri %q does not match the authorization request's %q", r.Form.Get("redirect_uri"), redirectTo)
		}

		// A provider that cannot validate the PKCE proof refuses the exchange;
		// the dashboard must turn that into a failed sign-in.
		if idp.rejectExchange {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}

		if !verifierOK {
			t.Errorf("/token: no code_verifier matching the challenge from /auth")
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}

		now := time.Now()
		claims := map[string]any{
			"iss": idp.srv.URL,
			"aud": "dashboard",
			"sub": "user-1",
			"iat": now.Unix(),
			"exp": now.Add(time.Hour).Unix(),
		}
		if idp.name != "" {
			claims["name"] = idp.name
		}
		if idp.email != "" {
			claims["email"] = idp.email
		}
		if idp.emailVerified != nil {
			claims["email_verified"] = *idp.emailVerified
		}

		writeJSONDoc(t, w, map[string]any{
			"access_token": "at",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idp.signIDToken(claims),
		})
	})

	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)

	return idp
}

// signIDToken signs the claims with RS256, exactly as a real provider would.
func (idp *fakeIDP) signIDToken(claims map[string]any) string {
	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	if err != nil {
		idp.t.Errorf("marshaling the JWT header: %v", err)
		return ""
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		idp.t.Errorf("marshaling the JWT claims: %v", err)
		return ""
	}

	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, idp.key, crypto.SHA256, digest[:])
	if err != nil {
		idp.t.Errorf("signing the ID token: %v", err)
		return ""
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func challengeFromVerifier(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func writeJSONDoc(t *testing.T, w http.ResponseWriter, doc any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(doc); err != nil {
		t.Errorf("writing a JSON response: %v", err)
	}
}

// newDashboardServer serves the auth routes of one manager, with the SPA shell
// standing in for web.Server.HandleIndex.
func newDashboardServer(t *testing.T, authMgr *AuthManager) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/session", authMgr.HandleSession)
	mux.HandleFunc("/api/login", authMgr.HandleLogin)
	mux.HandleFunc("/api/oidc/login", authMgr.HandleOIDCLogin)
	mux.HandleFunc("/api/oidc/callback", authMgr.HandleOIDCCallback)
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("spa shell"))
	})

	ts := httptest.NewServer(authMgr.Middleware(mux))
	t.Cleanup(ts.Close)
	return ts
}

// hop is one request in a manually walked redirect chain.
type hop struct {
	url      string
	response *http.Response
}

// walk follows redirects by hand so the intermediate callback URL stays visible.
// It uses a copy of the client that does not follow redirects itself, sharing
// the cookie jar so the session survives the walk. The last hop's body is left
// open for the caller.
func walk(t *testing.T, client *http.Client, start string) []hop {
	t.Helper()

	manual := *client
	manual.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	var hops []hop
	current := start

	for range 10 {
		resp, err := manual.Get(current)
		if err != nil {
			t.Fatalf("GET %s failed: %v", current, err)
		}
		hops = append(hops, hop{url: current, response: resp})

		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			return hops
		}

		location := resp.Header.Get("Location")
		resp.Body.Close()
		if location == "" {
			t.Fatalf("redirect from %s without a Location header", current)
		}

		base, err := url.Parse(current)
		if err != nil {
			t.Fatalf("unparsable URL %q: %v", current, err)
		}
		next, err := base.Parse(location)
		if err != nil {
			t.Fatalf("unparsable Location %q: %v", location, err)
		}
		current = next.String()
	}

	t.Fatal("redirect chain longer than 10 hops")
	return nil
}

// callbackHop returns the hop that hit the dashboard's callback, whose Location
// header carries the outcome. walk already closed its body.
func callbackHop(t *testing.T, hops []hop, baseURL string) hop {
	t.Helper()

	for _, h := range hops {
		if strings.HasPrefix(h.url, baseURL+"/api/oidc/callback") {
			return h
		}
	}
	t.Fatal("the flow never reached /api/oidc/callback")
	return hop{}
}

// oidcDashboard builds a dashboard with SSO only, plus a browser-like client.
func oidcDashboard(t *testing.T, idp *fakeIDP) (*httptest.Server, *http.Client) {
	t.Helper()

	authMgr, err := NewAuthManager(AuthConfig{
		OIDC: &OIDCConfig{
			IssuerURL:     idp.srv.URL,
			ClientID:      "dashboard",
			ClientSecret:  "s",
			AllowedEmails: []string{"Jane.Doe@Example.Com"}, // mixed case on purpose
		},
		SessionSecret: testSessionSecret,
	})
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	ts := newDashboardServer(t, authMgr)
	idp.wantRedirectHost = strings.TrimPrefix(ts.URL, "http://")

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building the cookie jar: %v", err)
	}
	return ts, &http.Client{Jar: jar}
}

func TestOIDCLoginFlow(t *testing.T) {
	idp := newFakeIDP(t)
	ts, client := oidcDashboard(t, idp)

	// A browser-like client follows every hop: dashboard -> IdP -> callback ->
	// the SPA shell.
	resp, err := client.Get(ts.URL + "/api/oidc/login")
	if err != nil {
		t.Fatalf("GET /api/oidc/login failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected to land on the SPA shell, got %d", resp.StatusCode)
	}
	if got := resp.Request.URL.Path; got != "/" {
		t.Fatalf("expected to land on /, got %q", got)
	}

	// The session carries the identity from the ID token.
	info := getSessionInfo(t, client, ts.URL)
	if !info.Authenticated {
		t.Fatal("expected an authenticated session after the SSO flow")
	}
	if info.User == nil {
		t.Fatal("expected the session to carry a user")
	}
	if info.User.Email != "jane.doe@example.com" {
		t.Errorf("expected the email from the ID token, got %q", info.User.Email)
	}
	if info.User.Name != "Jane Doe" {
		t.Errorf("expected the name from the ID token, got %q", info.User.Name)
	}
	if info.User.Monogram != "JD" {
		t.Errorf("expected monogram JD, got %q", info.User.Monogram)
	}
	if info.PasswordLogin {
		t.Error("password sign-in is not configured but is advertised")
	}
	if !info.OIDCLogin {
		t.Error("expected oidc_login to be advertised")
	}

	// And the data endpoint is reachable with that session.
	dataResp, err := client.Get(ts.URL + "/api/data")
	if err != nil {
		t.Fatalf("GET /api/data failed: %v", err)
	}
	defer dataResp.Body.Close()
	if dataResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /api/data, got %d", dataResp.StatusCode)
	}
}

func TestOIDCCallbackRefusals(t *testing.T) {
	t.Run("unlisted email", func(t *testing.T) {
		idp := newFakeIDP(t)
		idp.email = "mallory@example.com"
		idp.name = "Mallory"
		ts, client := oidcDashboard(t, idp)

		hops := walk(t, client, ts.URL+"/api/oidc/login")
		defer hops[len(hops)-1].response.Body.Close()

		callback := callbackHop(t, hops, ts.URL)
		if callback.response.StatusCode != http.StatusSeeOther {
			t.Fatalf("expected 303 from the callback, got %d", callback.response.StatusCode)
		}
		if loc := callback.response.Header.Get("Location"); loc != "/?auth_error=forbidden" {
			t.Fatalf("expected /?auth_error=forbidden, got %q", loc)
		}
		if info := getSessionInfo(t, client, ts.URL); info.Authenticated {
			t.Fatal("a refused sign-in must not create a session")
		}
	})

	t.Run("unverified email", func(t *testing.T) {
		idp := newFakeIDP(t)
		verified := false
		idp.emailVerified = &verified
		ts, client := oidcDashboard(t, idp)

		hops := walk(t, client, ts.URL+"/api/oidc/login")
		defer hops[len(hops)-1].response.Body.Close()

		if loc := callbackHop(t, hops, ts.URL).response.Header.Get("Location"); loc != "/?auth_error=forbidden" {
			t.Fatalf("expected /?auth_error=forbidden, got %q", loc)
		}
	})

	t.Run("state replay", func(t *testing.T) {
		idp := newFakeIDP(t)
		ts, client := oidcDashboard(t, idp)

		hops := walk(t, client, ts.URL+"/api/oidc/login")
		defer hops[len(hops)-1].response.Body.Close()

		if last := hops[len(hops)-1]; last.url != ts.URL+"/" {
			t.Fatalf("expected a successful flow first, ended at %q", last.url)
		}
		callback := callbackHop(t, hops, ts.URL).url

		replay := walk(t, client, callback)
		defer replay[len(replay)-1].response.Body.Close()

		if replay[0].response.StatusCode != http.StatusSeeOther {
			t.Fatalf("expected 303 for the replay, got %d", replay[0].response.StatusCode)
		}
		if loc := replay[0].response.Header.Get("Location"); loc != "/?auth_error=failed" {
			t.Fatalf("expected /?auth_error=failed, got %q", loc)
		}
	})

	t.Run("pkce rejected", func(t *testing.T) {
		idp := newFakeIDP(t)
		idp.rejectExchange = true
		ts, client := oidcDashboard(t, idp)

		hops := walk(t, client, ts.URL+"/api/oidc/login")
		defer hops[len(hops)-1].response.Body.Close()

		if loc := callbackHop(t, hops, ts.URL).response.Header.Get("Location"); loc != "/?auth_error=failed" {
			t.Fatalf("expected /?auth_error=failed, got %q", loc)
		}
		if info := getSessionInfo(t, client, ts.URL); info.Authenticated {
			t.Fatal("a failed exchange must not create a session")
		}
	})
}

// getSessionInfo reads /api/session with the client's cookies.
func getSessionInfo(t *testing.T, client *http.Client, baseURL string) sessionInfo {
	t.Helper()

	resp, err := client.Get(baseURL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/session: expected 200, got %d", resp.StatusCode)
	}

	var info sessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("GET /api/session: bad JSON: %v", err)
	}
	return info
}
