package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
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

func main() {
	amiHost := flag.String("ami-host", getEnvOrDefault("AMI_HOST", "127.0.0.1"), "Asterisk AMI host or IP")
	amiPort := flag.String("ami-port", getEnvOrDefault("AMI_PORT", "5038"), "Asterisk AMI port")
	amiUser := flag.String("ami-user", getEnvOrDefault("AMI_USER", "admin"), "AMI username")
	amiPass := flag.String("ami-pass", getEnvOrDefault("AMI_PASS", "admin"), "AMI password")
	webPort := flag.String("http", getEnvOrDefault("HTTP_PORT", "8080"), "Web server port")
	authUser := flag.String("auth-user", getEnvOrDefault("AUTH_USER", "admin"), "Dashboard username")
	authPass := flag.String("auth-pass", getEnvOrDefault("AUTH_PASS", "admin"), "Dashboard password")
	flag.Parse()

	amiAddr := fmt.Sprintf("%s:%s", *amiHost, *amiPort)
	log.Printf("Connecting to Asterisk AMI: %s (user: %s)...", amiAddr, *amiUser)

	cmdClient := ami.NewClient(amiAddr, *amiUser, *amiPass)
	streamClient := ami.NewStreamClient(amiAddr, *amiUser, *amiPass)

	srv := service.NewAsteriskService(cmdClient)

	// WebSocket Hub
	hub := web.NewHub()
	go hub.Run()

	// Trigger refresh and broadcast to all connected WebSocket clients
	refreshAndBroadcast := func() {
		if err := srv.Refresh(); err == nil {
			hub.Broadcast(srv.GetData())
		}
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
				time.Sleep(2 * time.Second)
			}
		}
	}()

	mux := http.NewServeMux()
	webHandler := web.NewServer(srv, hub)
	authMgr := web.NewAuthManager(*authUser, *authPass)

	mux.HandleFunc("/login", authMgr.HandleLogin)
	mux.HandleFunc("/logout", authMgr.HandleLogout)
	mux.HandleFunc("/", webHandler.HandleIndex)
	mux.HandleFunc("/api/data", webHandler.HandleData)
	mux.HandleFunc("/ws", webHandler.HandleWS)

	handler := authMgr.Middleware(mux)

	listenAddr := fmt.Sprintf(":%s", *webPort)
	log.Printf("Asterisk Web Dashboard live at: http://localhost%s (auth user: %s)", listenAddr, *authUser)
	if err := http.ListenAndServe(listenAddr, handler); err != nil {
		log.Fatalf("Web server stopped: %v", err)
	}
}
