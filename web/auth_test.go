package web

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthManager(t *testing.T) {
	mux := http.NewServeMux()
	authMgr := NewAuthManager("admin", "secret")

	mux.HandleFunc("/login", authMgr.HandleLogin)
	mux.HandleFunc("/logout", authMgr.HandleLogout)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("dashboard home"))
	})
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	handler := authMgr.Middleware(mux)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 1. Unauthenticated request to "/" redirects to /login
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("expected Location /login, got %q", loc)
	}

	// 2. Unauthenticated request to /api/data gives 401
	resp, err = client.Get(ts.URL + "/api/data")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for api, got %d", resp.StatusCode)
	}

	// 3. Failed login attempt
	form := url.Values{}
	form.Set("username", "admin")
	form.Set("password", "wrongpass")
	resp, err = client.Post(ts.URL+"/login", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("login post failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on bad credentials, got %d", resp.StatusCode)
	}

	// 4. Successful login
	form.Set("password", "secret")
	resp, err = client.Post(ts.URL+"/login", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("login post failed: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect after login, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to /, got %q", loc)
	}

	// 5. Access dashboard with cookie
	resp, err = client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("access with session failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after login, got %d", resp.StatusCode)
	}

	// 6. Access API with cookie
	resp, err = client.Get(ts.URL + "/api/data")
	if err != nil {
		t.Fatalf("api access failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on api after login, got %d", resp.StatusCode)
	}

	// 7. Logout
	resp, err = client.Get(ts.URL + "/logout")
	if err != nil {
		t.Fatalf("logout request failed: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect after logout, got %d", resp.StatusCode)
	}

	// 8. Access dashboard after logout -> redirected to /login
	resp, err = client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("request after logout failed: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect after logout, got %d", resp.StatusCode)
	}
}
