package service

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asterisk-dashboard/ami"
)

// testAMI is a minimal in-process Asterisk AMI server: enough for Refresh() and
// getEndpointConfig(). PJSIPShowEndpoints answers with a fresh endpoint name on
// every query, so a repeated Refresh() always misses (and therefore writes) the
// per-endpoint config cache.
type testAMI struct {
	ln net.Listener
	ep int64
}

func startTestAMI(t *testing.T) *testAMI {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &testAMI{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *testAMI) addr() string { return s.ln.Addr().String() }

func (s *testAMI) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	_, _ = c.Write([]byte("Asterisk Call Manager/5.0.4\r\n"))
	r := bufio.NewReader(c)
	for {
		h, err := readTestPacket(r)
		if err != nil {
			return
		}
		id := h["actionid"]
		switch strings.ToLower(h["action"]) {
		case "login":
			_, _ = c.Write([]byte("Response: Success\r\nMessage: Authentication accepted\r\n\r\n"))
		case "logoff":
			return
		case "corestatus":
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\n"+
				"CoreCurrentCalls: 2\r\nCoreProcessedCalls: 42\r\nCoreReloadTime: 07:15:30\r\n\r\n", id)
		case "coresettings":
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\nAsteriskVersion: 20.5.0\r\n\r\n", id)
		case "command":
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\n"+
				"Output: System uptime: 1 day, 2 hours, 3 minutes, 4 seconds\r\n"+
				"Output: 4 active channels\r\nOutput: 2 active calls\r\nOutput: 42 calls processed\r\n\r\n", id)
		case "coreshowchannels":
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\n\r\n"+
				"Event: CoreShowChannel\r\nActionID: %s\r\nChannel: PJSIP/1-1\r\nChannelStateDesc: Up\r\n\r\n"+
				"Event: CoreShowChannelsComplete\r\nActionID: %s\r\n\r\n", id, id, id)
		case "pjsipshowendpoints":
			name := fmt.Sprintf("ep-%d", atomic.AddInt64(&s.ep, 1))
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\n\r\n"+
				"Event: EndpointList\r\nActionID: %s\r\nObjectName: %s\r\nDeviceState: Not in use\r\nActiveChannels: 0\r\nAor: %s\r\n\r\n"+
				"Event: EndpointListComplete\r\nActionID: %s\r\n\r\n", id, id, name, name, id)
		case "pjsipshowendpoint":
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\n\r\n"+
				"Event: EndpointDetail\r\nActionID: %s\r\nObjectName: %s\r\nContext: from-internal\r\nAllow: (ulaw)\r\n\r\n"+
				"Event: EndpointDetailComplete\r\nActionID: %s\r\n\r\n", id, id, h["endpoint"], id)
		case "pjsipshowcontacts":
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\n\r\n"+
				"Event: ContactListComplete\r\nActionID: %s\r\n\r\n", id, id)
		case "pjsipshowregistrationsoutbound":
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\n\r\n"+
				"Event: OutboundRegistrationDetailComplete\r\nActionID: %s\r\n\r\n", id, id)
		default:
			_, _ = fmt.Fprintf(c, "Response: Error\r\nActionID: %s\r\nMessage: not implemented\r\n\r\n", id)
		}
	}
}

func readTestPacket(r *bufio.Reader) (map[string]string, error) {
	h := make(map[string]string)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(h) == 0 {
				continue
			}
			return h, nil
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			h[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
}

func dialTestAMI(t *testing.T, srv *testAMI) *ami.Client {
	t.Helper()
	c := ami.NewClient(srv.addr(), "admin", "admin")
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return c
}

func TestRefreshOnlineReportsFreshData(t *testing.T) {
	srv := startTestAMI(t)
	c := dialTestAMI(t, srv)
	defer c.Close()
	s := NewAsteriskService(c)

	if err := s.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	d := s.GetData()
	if d.Global.Status != "Online" {
		t.Errorf("status = %q; want Online", d.Global.Status)
	}
	if d.Global.Version != "Asterisk 20.5.0" {
		t.Errorf("version = %q; want Asterisk 20.5.0", d.Global.Version)
	}
	if d.Global.Uptime != "1d 2h 3m" {
		t.Errorf("uptime = %q; want 1d 2h 3m", d.Global.Uptime)
	}
	if d.Global.ActiveChannels != "4" || d.Global.ActiveCalls != "2" {
		t.Errorf("counters = channels %q calls %q; want 4/2", d.Global.ActiveChannels, d.Global.ActiveCalls)
	}
	if len(d.Endpoints) != 1 {
		t.Errorf("endpoints = %d; want 1", len(d.Endpoints))
	}
}

func TestRefreshOfflinePreservesLastGoodData(t *testing.T) {
	srv := startTestAMI(t)
	c := dialTestAMI(t, srv)
	s := NewAsteriskService(c)

	if err := s.Refresh(); err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}
	good := s.GetData()
	if good.Global.Status != "Online" {
		t.Fatalf("initial status = %q; want Online", good.Global.Status)
	}

	// Cross a second boundary so a buggy "now()" LastUpdated would differ.
	time.Sleep(1100 * time.Millisecond)

	// AMI loss: every call now fails with "no active AMI connection".
	c.Close()
	if err := s.Refresh(); err == nil {
		t.Fatal("Refresh after AMI loss returned nil; want an error")
	}

	off := s.GetData()
	if off.Global.Status != "Offline" {
		t.Errorf("status = %q; want Offline", off.Global.Status)
	}
	if off.Global.LastUpdated != good.Global.LastUpdated {
		t.Errorf("last_updated advanced while offline: %q -> %q; want it frozen", good.Global.LastUpdated, off.Global.LastUpdated)
	}
	if off.Global.Version != good.Global.Version {
		t.Errorf("version = %q; want last good %q", off.Global.Version, good.Global.Version)
	}
	if off.Global.Uptime != good.Global.Uptime {
		t.Errorf("uptime = %q; want last good %q", off.Global.Uptime, good.Global.Uptime)
	}
	if off.Global.ActiveChannels != good.Global.ActiveChannels || off.Global.ActiveCalls != good.Global.ActiveCalls {
		t.Errorf("counters changed offline: %q/%q; want %q/%q",
			off.Global.ActiveChannels, off.Global.ActiveCalls, good.Global.ActiveChannels, good.Global.ActiveCalls)
	}
	if len(off.Endpoints) != len(good.Endpoints) {
		t.Errorf("endpoints = %d; want last good %d", len(off.Endpoints), len(good.Endpoints))
	}

	// Recovery: reconnecting the same client restores real data.
	if err := c.Connect(); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer c.Close()
	if err := s.Refresh(); err != nil {
		t.Fatalf("Refresh after reconnect: %v", err)
	}
	if got := s.GetData().Global.Status; got != "Online" {
		t.Errorf("status after reconnect = %q; want Online", got)
	}
}

// TestRefreshConcurrentEndpointCache exercises the exact call pattern main.go
// uses: Refresh() from the 2s command loop and from the AMI event handler at
// the same time. Run with -race; it fails if epConfigCache is not locked.
func TestRefreshConcurrentEndpointCache(t *testing.T) {
	srv := startTestAMI(t)
	c := dialTestAMI(t, srv)
	defer c.Close()
	s := NewAsteriskService(c)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 15 {
				_ = s.Refresh()
			}
		}()
	}
	wg.Wait()

	if got := s.GetData().Global.Status; got != "Online" {
		t.Errorf("status = %q; want Online", got)
	}
}
