package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// oidcStateDuration is how long a started authorization request stays valid.
const oidcStateDuration = 10 * time.Minute

// OIDCConfig is the deployment-side SSO configuration. All four values are
// required together: the ID token only proves the account, AllowedEmails is the
// authorization, so an empty allow-list can never mean "everyone".
type OIDCConfig struct {
	IssuerURL     string
	ClientID      string
	ClientSecret  string
	AllowedEmails []string // parsed by ParseAllowedEmails, >= 1 entry
}

// oidcClient is the OAuth2/OIDC client, built once at startup: discovery is
// fetched here, so a typo'd issuer URL fails at boot rather than at the first
// sign-in. The JWKS is fetched lazily, on the first token verification.
type oidcClient struct {
	oauth2Cfg oauth2.Config
	verifier  *oidc.IDTokenVerifier
	allowed   map[string]bool // lowercased allowed emails
}

func newOIDCClient(cfg *OIDCConfig) (*oidcClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// NewProvider fetches /.well-known/openid-configuration and checks that the
	// document's issuer matches the configured one.
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery for %s: %w", cfg.IssuerURL, err)
	}

	allowed := make(map[string]bool, len(cfg.AllowedEmails))
	for _, email := range cfg.AllowedEmails {
		allowed[strings.ToLower(strings.TrimSpace(email))] = true
	}

	return &oidcClient{
		oauth2Cfg: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		allowed:  allowed,
	}, nil
}

// ParseAllowedEmails splits an OIDC_ALLOWED_EMAILS value into the lowercase
// allow-list. It is exported so main's startup validation applies exactly the
// same rule.
func ParseAllowedEmails(v string) []string {
	var emails []string
	for _, part := range strings.Split(v, ",") {
		if email := strings.ToLower(strings.TrimSpace(part)); email != "" {
			emails = append(emails, email)
		}
	}
	return emails
}

// oidcState is a started authorization request: the PKCE verifier is held
// server-side until the callback, and the entry is single-use.
type oidcState struct {
	verifier string
	expires  time.Time
}

// HandleOIDCLogin starts the authorization code flow with PKCE. It is a plain
// navigation target, not a fetch: the browser leaves the SPA here.
func (a *AuthManager) HandleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	if a.oidc == nil {
		http.Redirect(w, r, "/?auth_error=failed", http.StatusSeeOther)
		return
	}

	state := a.generateToken()
	verifier := oauth2.GenerateVerifier()

	now := time.Now()
	a.mu.Lock()
	for s, pending := range a.authStates {
		if now.After(pending.expires) {
			delete(a.authStates, s)
		}
	}
	a.authStates[state] = oidcState{verifier: verifier, expires: now.Add(oidcStateDuration)}
	a.mu.Unlock()

	cfg := a.oidc.oauth2Cfg
	cfg.RedirectURL = externalBaseURL(r) + "/api/oidc/callback"
	http.Redirect(w, r, cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), http.StatusSeeOther)
}

// HandleOIDCCallback finishes the flow: it exchanges the code (proving the PKCE
// verifier), verifies the ID token and authorizes the account against the
// allow-list. Every outcome is a redirect back to the SPA, which renders the
// reason from ?auth_error=.
func (a *AuthManager) HandleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		http.Redirect(w, r, "/?auth_error=failed", http.StatusSeeOther)
		return
	}

	fail := func(step string, err error) {
		log.Printf("[OIDC] %s: %v", step, err)
		http.Redirect(w, r, "/?auth_error=failed", http.StatusSeeOther)
	}
	forbidden := func(step string, err error) {
		log.Printf("[OIDC] %s: %v", step, err)
		http.Redirect(w, r, "/?auth_error=forbidden", http.StatusSeeOther)
	}

	// 1. The IdP can refuse the request outright.
	if code := r.URL.Query().Get("error"); code != "" {
		fail("authorization error", fmt.Errorf("%s: %s", code, r.URL.Query().Get("error_description")))
		return
	}

	// 2. The state ties this callback to the request that started the flow, and
	//    is single-use: a replayed callback finds nothing.
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		fail("callback", fmt.Errorf("missing state or code"))
		return
	}

	a.mu.Lock()
	pending, known := a.authStates[state]
	delete(a.authStates, state)
	a.mu.Unlock()

	if !known || time.Now().After(pending.expires) {
		fail("state", fmt.Errorf("unknown or expired state"))
		return
	}

	// 3. Exchange the code, proving possession of the PKCE verifier. Both legs
	//    derive redirect_uri the same way, so the IdP sees identical values.
	cfg := a.oidc.oauth2Cfg
	cfg.RedirectURL = externalBaseURL(r) + "/api/oidc/callback"

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(pending.verifier))
	if err != nil {
		fail("token exchange", err)
		return
	}

	// 4. Verify the ID token: signature against the JWKS, issuer, audience, exp.
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		fail("token response", fmt.Errorf("no id_token"))
		return
	}

	idToken, err := a.oidc.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		fail("id token verification", err)
		return
	}

	// 5. Claims. email_verified is not hard-required because some IdPs omit it;
	//    present-and-false is refused.
	var claims struct {
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
		EmailVerified     *bool  `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		fail("claims", err)
		return
	}

	// 6. The ID token proves the account; the allow-list authorizes it.
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		fail("claims", fmt.Errorf("the ID token carries no email address"))
		return
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		forbidden("sign-in refused", fmt.Errorf("%s has an unverified email address", email))
		return
	}
	if !a.oidc.allowed[email] {
		forbidden("sign-in refused", fmt.Errorf("%s is not in OIDC_ALLOWED_EMAILS", email))
		return
	}

	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}

	a.startSession(w, sessionUser{Name: name, Email: claims.Email, Monogram: monogramOf(name, claims.Email)})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// externalBaseURL derives the public base URL of the request, so both legs of
// the flow agree on redirect_uri (the IdP compares them). A TLS-terminating
// proxy must pass X-Forwarded-Proto and the public Host through.
func externalBaseURL(r *http.Request) string {
	scheme := "http"
	switch {
	case r.TLS != nil:
		scheme = "https"
	case r.Header.Get("X-Forwarded-Proto") != "":
		scheme = r.Header.Get("X-Forwarded-Proto")
	}
	return scheme + "://" + r.Host
}
