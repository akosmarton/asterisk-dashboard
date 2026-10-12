package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// hubTestServer wraps Hub.HandleWS and records the time each handler spent
// before returning (handshake + initial-snapshot enqueue + registration).
func hubTestServer(hub *Hub, onConnect func() []byte) (*httptest.Server, *atomic.Int64) {
	var dur atomic.Int64
	hts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		hub.HandleWS(w, r, onConnect)
		dur.Store(int64(time.Since(t0)))
	}))
	return hts, &dur
}

func hubWSURL(hts *httptest.Server) string {
	return "ws" + strings.TrimPrefix(hts.URL, "http")
}

// TestHubConcurrentConnectAndBroadcast reproduces the original crash shape:
// broadcasts in flight while clients connect. It must not panic or report a
// data race (run with -race).
func TestHubConcurrentConnectAndBroadcast(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	hts, _ := hubTestServer(hub, func() []byte { return []byte(`{"n":-1}`) })
	url := hubWSURL(hts)

	var stop atomic.Bool
	var bwg sync.WaitGroup
	bwg.Add(1)
	go func() {
		defer bwg.Done()
		for i := 0; !stop.Load(); i++ {
			hub.Broadcast(map[string]int{"n": i})
		}
	}()

	var mu sync.Mutex
	var conns []*websocket.Conn
	var dwg sync.WaitGroup
	for range 50 {
		dwg.Add(1)
		go func() {
			defer dwg.Done()
			c, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}
		}()
	}

	time.Sleep(1500 * time.Millisecond)
	stop.Store(true)
	bwg.Wait()

	mu.Lock()
	for _, c := range conns {
		_ = c.Close()
	}
	n := len(conns)
	mu.Unlock()
	dwg.Wait()
	hts.Close()
	t.Logf("connected %d clients concurrently with broadcasts, no panic", n)
}

// TestHubBroadcastNeverBlocks asserts Broadcast returns promptly even with
// clients that completed the handshake and then stopped reading.
func TestHubBroadcastNeverBlocks(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	hts, _ := hubTestServer(hub, func() []byte { return []byte(`{"init":true}`) })
	url := hubWSURL(hts)

	stuck := make([]*websocket.Conn, 0, 5)
	for range 5 {
		c, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial stuck client: %v", err)
		}
		stuck = append(stuck, c) // never read -> socket buffers fill
	}
	time.Sleep(100 * time.Millisecond)

	payload := strings.Repeat("x", 64*1024)
	var maxD time.Duration
	start := time.Now()
	for range 200 {
		t0 := time.Now()
		hub.Broadcast(payload)
		if d := time.Since(t0); d > maxD {
			maxD = d
		}
	}
	total := time.Since(start)
	t.Logf("200 Broadcast calls with 5 stuck clients: max single=%v total=%v", maxD, total)
	if maxD > 200*time.Millisecond {
		t.Fatalf("Broadcast blocked on a stuck client: max=%v", maxD)
	}

	for _, c := range stuck {
		_ = c.Close()
	}
	hts.Close()
}

// TestHubRegisterNotBlockedByStuckClients asserts a brand-new GET /ws client is
// served promptly while other clients are stuck.
func TestHubRegisterNotBlockedByStuckClients(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	hts, dur := hubTestServer(hub, func() []byte { return []byte(`{"init":true}`) })
	url := hubWSURL(hts)

	stuck := make([]*websocket.Conn, 0, 5)
	for range 5 {
		c, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial stuck client: %v", err)
		}
		stuck = append(stuck, c)
	}
	time.Sleep(100 * time.Millisecond)

	t0 := time.Now()
	b, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial new client: %v", err)
	}
	_ = b.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := b.ReadMessage(); err != nil {
		t.Fatalf("new client did not receive initial snapshot: %v", err)
	}
	waited := time.Since(t0)
	handlerLat := time.Duration(dur.Load())
	t.Logf("new client with 5 stuck clients: dial+snapshot=%v handler=%v", waited, handlerLat)
	if waited > 2*time.Second {
		t.Fatalf("new client blocked behind stuck clients: %v", waited)
	}
	if handlerLat > 2*time.Second {
		t.Fatalf("HandleWS handler blocked behind stuck clients: %v", handlerLat)
	}

	_ = b.Close()
	for _, c := range stuck {
		_ = c.Close()
	}
	hts.Close()
}

// TestHubSlowClientDoesNotStallHealthyClient asserts a healthy reader keeps
// receiving broadcasts while another client is stuck.
func TestHubSlowClientDoesNotStallHealthyClient(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	hts, _ := hubTestServer(hub, func() []byte { return []byte(`{"init":true}`) })
	url := hubWSURL(hts)

	stuck, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial stuck client: %v", err)
	}
	healthy, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial healthy client: %v", err)
	}

	// Count only the big broadcast payloads (the tiny initial snapshot is
	// ignored). 10 broadcasts is below the 16-deep queue, so a healthy client
	// must see every one of them.
	const sent = 10
	payload := strings.Repeat("y", 1024)

	got := make(chan struct{}, 64)
	go func() {
		defer close(got)
		for {
			_, msg, err := healthy.ReadMessage()
			if err != nil {
				return
			}
			if len(msg) > 100 {
				select {
				case got <- struct{}{}:
				default:
				}
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	for range sent {
		hub.Broadcast(payload)
	}

	count := 0
	deadline := time.After(5 * time.Second)
loop:
	for count < sent {
		select {
		case _, ok := <-got:
			if !ok {
				break loop
			}
			count++
		case <-deadline:
			break loop
		}
	}
	t.Logf("healthy client received %d/%d broadcasts while a stuck client existed", count, sent)
	if count < sent {
		t.Fatalf("healthy client starved by stuck client: received %d/%d", count, sent)
	}

	_ = healthy.Close()
	_ = stuck.Close()
	hts.Close()
}
