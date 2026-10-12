package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"asterisk-dashboard/ami"
	"asterisk-dashboard/service"
	"asterisk-dashboard/web"
)

func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// The SPA build. `all:` includes dotfiles, so the committed ui/dist/.gitkeep
// keeps the pattern valid on a fresh clone before the first `npm run build` —
// the build output itself is not committed.
//
//go:embed all:ui/dist
var uiFS embed.FS

// uiAssets returns the embedded SPA build.
func uiAssets() fs.FS {
	assets, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		panic(err)
	}
	return assets
}

func main() {
	amiHost := flag.String("ami-host", getEnvOrDefault("AMI_HOST", "127.0.0.1"), "Asterisk AMI host or IP")
	amiPort := flag.String("ami-port", getEnvOrDefault("AMI_PORT", "5038"), "Asterisk AMI port")
	amiUser := flag.String("ami-user", getEnvOrDefault("AMI_USER", "admin"), "AMI username")
	amiPass := flag.String("ami-pass", getEnvOrDefault("AMI_PASS", "admin"), "AMI password")
	webPort := flag.String("http", getEnvOrDefault("HTTP_PORT", "8080"), "Web server port")
	adminUser := flag.String("admin-user", getEnvOrDefault("ADMIN_USER", ""), "Dashboard username; with -admin-pass enables password sign-in")
	adminPass := flag.String("admin-pass", getEnvOrDefault("ADMIN_PASSWORD", ""), "Dashboard password; with -admin-user enables password sign-in")
	devMode := flag.Bool("dev", getEnvOrDefault("DEV", "") != "", "Serve frontend assets from ui/dist on disk instead of the embedded build")
	flag.Parse()

	// SSO is env-only: the four values are deployment/secret configuration, not
	// per-run CLI switches.
	oidcIssuer := os.Getenv("OIDC_ISSUER_URL")
	oidcClientID := os.Getenv("OIDC_CLIENT_ID")
	oidcClientSecret := os.Getenv("OIDC_CLIENT_SECRET")
	oidcAllowed := os.Getenv("OIDC_ALLOWED_EMAILS")

	// Sign-in configuration is validated before anything listens: a half-set
	// password pair or a half-set OIDC block would otherwise fail silently, at
	// the first sign-in attempt.
	oidcSet := oidcIssuer != "" || oidcClientID != "" || oidcClientSecret != "" || oidcAllowed != ""
	if oidcSet && (oidcIssuer == "" || oidcClientID == "" || oidcClientSecret == "" || oidcAllowed == "") {
		log.Fatalf("invalid auth configuration: OIDC is partially configured — set all of OIDC_ISSUER_URL, OIDC_CLIENT_ID, OIDC_CLIENT_SECRET and OIDC_ALLOWED_EMAILS, or none of them")
	}
	oidcEmails := web.ParseAllowedEmails(oidcAllowed)
	if oidcSet && len(oidcEmails) == 0 {
		log.Fatalf("invalid auth configuration: OIDC_ALLOWED_EMAILS lists no email address; an empty allow-list can never mean \"everyone\"")
	}
	if (*adminUser == "") != (*adminPass == "") {
		log.Fatalf("invalid auth configuration: ADMIN_USER and ADMIN_PASSWORD must be set together")
	}
	if *adminUser == "" && !oidcSet {
		log.Println("WARNING: no sign-in method is configured (set ADMIN_USER/ADMIN_PASSWORD and/or the OIDC_* variables); the dashboard is reachable only with an API key in front of it")
	}

	amiAddr := fmt.Sprintf("%s:%s", *amiHost, *amiPort)
	log.Printf("Connecting to Asterisk AMI: %s (user: %s)...", amiAddr, *amiUser)

	cmdClient := ami.NewClient(amiAddr, *amiUser, *amiPass)
	streamClient := ami.NewStreamClient(amiAddr, *amiUser, *amiPass)

	srv := service.NewAsteriskService(cmdClient)

	// WebSocket Hub
	hub := web.NewHub()
	go hub.Run()

	// AMI connectivity is logged on transitions only. The event boot burst
	// triggers refreshAndBroadcast once per event, so logging every failure
	// would spam one line per event for the whole re-dial window.
	var amiState struct {
		mu        sync.Mutex
		status    string
		offlineAt time.Time
	}

	// Trigger refresh and broadcast to all connected WebSocket clients. A
	// failed refresh is broadcast too: Refresh() republishes the last good
	// snapshot with Status "Offline", so the badge flips instead of the UI
	// silently keeping a stale "Online".
	refreshAndBroadcast := func() {
		err := srv.Refresh()

		amiState.mu.Lock()
		switch {
		case err != nil && amiState.status != "Offline":
			amiState.status = "Offline"
			amiState.offlineAt = time.Now()
			log.Printf("[AMI] Offline: %v", err)
		case err == nil && amiState.status != "Online":
			if amiState.offlineAt.IsZero() {
				log.Println("[AMI] Online.")
			} else {
				log.Printf("[AMI] Back online after %s.", time.Since(amiState.offlineAt).Round(time.Second))
			}
			amiState.status = "Online"
			amiState.offlineAt = time.Time{}
		}
		amiState.mu.Unlock()

		hub.Broadcast(srv.GetData())
	}

	// 1. Dedicated AMI Event Stream goroutine (instant reactivity)
	go func() {
		for {
			err := streamClient.ConnectAndListen(func(event string, headers map[string]string) {
				// Feed event to call tracker
				historyChanged := srv.GetTracker().HandleEvent(event, headers)

				ev := strings.ToLower(event)
				// React instantly to state changes, calls, registrations, device states
				if historyChanged ||
					strings.Contains(ev, "channel") ||
					strings.Contains(ev, "call") ||
					strings.Contains(ev, "bridge") ||
					strings.Contains(ev, "devicestate") ||
					strings.Contains(ev, "endpoint") ||
					strings.Contains(ev, "contact") ||
					strings.Contains(ev, "registry") ||
					strings.Contains(ev, "registration") ||
					strings.Contains(ev, "hangup") ||
					strings.Contains(ev, "dial") ||
					strings.Contains(ev, "cdr") {
					refreshAndBroadcast()
				}
			})

			if err != nil {
				log.Printf("[AMI Event Stream] %v. Retrying in 4 seconds...", err)
				time.Sleep(4 * time.Second)
			}
		}
	}()

	// 2. Periodic sync & reconnect loop for cmdClient and heartbeat metrics
	go func() {
		for {
			err := cmdClient.Connect()
			if err != nil {
				log.Printf("[AMI Command Client Error] %v. Retrying in 5 seconds...", err)
				time.Sleep(5 * time.Second)
				continue
			}

			log.Println("[AMI Command Client] Connected.")

			for {
				refreshAndBroadcast()
				// A lost connection drops back to the outer loop, which
				// re-dials (with its 5s backoff). Without this the loop below
				// would spin forever against a nil connection and the command
				// client would never reconnect after an Asterisk restart.
				if !cmdClient.IsConnected() {
					break
				}
				time.Sleep(2 * time.Second)
			}
		}
	}()

	// Frontend: the embedded Vite build of ui/ by default, the working copy with
	// -dev so `npm run dev` (vite build --watch in ui/) is picked up without a Go
	// rebuild.
	assets := uiAssets()
	if *devMode {
		assets = os.DirFS(filepath.Join("ui", "dist"))
		log.Println("[Dev] Serving frontend assets from ui/dist — run `npm run dev` in ui/ to rebuild them.")
	}
	if _, err := fs.Stat(assets, "index.html"); err != nil {
		log.Fatal("Frontend build is missing: run `npm install && npm run build` in ui/.")
	}

	mux := http.NewServeMux()
	webHandler := web.NewServer(srv, hub, assets)

	authCfg := web.AuthConfig{User: *adminUser, Pass: *adminPass}
	if oidcSet {
		authCfg.OIDC = &web.OIDCConfig{
			IssuerURL:     oidcIssuer,
			ClientID:      oidcClientID,
			ClientSecret:  oidcClientSecret,
			AllowedEmails: oidcEmails,
		}
	}
	// Discovery failure is fatal on purpose: a typo'd issuer URL should surface
	// at boot, not at the first sign-in attempt.
	authMgr, err := web.NewAuthManager(authCfg)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}

	mux.HandleFunc("/api/session", authMgr.HandleSession)
	mux.HandleFunc("/api/login", authMgr.HandleLogin)
	mux.HandleFunc("/api/logout", authMgr.HandleLogout)
	mux.HandleFunc("/api/oidc/login", authMgr.HandleOIDCLogin)
	mux.HandleFunc("/api/oidc/callback", authMgr.HandleOIDCCallback)
	mux.HandleFunc("/api/data", webHandler.HandleData)
	mux.HandleFunc("/ws", webHandler.HandleWS)
	mux.HandleFunc("/favicon.svg", webHandler.HandleFavicon)
	mux.HandleFunc("/favicon.ico", webHandler.HandleFavicon)
	mux.Handle("/static/", http.StripPrefix("/static/", webHandler.StaticHandler()))
	// Everything else is the SPA shell: it asks /api/session and renders either
	// the dashboard or the login view.
	mux.HandleFunc("/", webHandler.HandleIndex)

	handler := authMgr.Middleware(mux)

	listenAddr := fmt.Sprintf(":%s", *webPort)

	var methods []string
	if *adminUser != "" {
		methods = append(methods, "password")
	}
	if oidcSet {
		methods = append(methods, "oidc")
	}
	signMethods := "none (locked)"
	if len(methods) > 0 {
		signMethods = strings.Join(methods, " + ")
	}

	log.Printf("Asterisk Web Dashboard live at: http://localhost%s (sign-in: %s)", listenAddr, signMethods)
	if err := http.ListenAndServe(listenAddr, handler); err != nil {
		log.Fatalf("Web server stopped: %v", err)
	}
}
