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
// per-endpoint config cache. It also reports the trunk endpoint wavecom, whose
// contact only the endpoint listing knows about.
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
				"Event: EndpointList\r\nActionID: %s\r\nObjectName: wavecom\r\nDeviceState: Not in use\r\nActiveChannels: 0\r\nAor: wavecom\r\n\r\n"+
				"Event: EndpointListComplete\r\nActionID: %s\r\n\r\n", id, id, name, name, id, id)
		case "pjsipshowendpoint":
			detail := fmt.Sprintf("Event: EndpointDetail\r\nActionID: %s\r\nObjectName: %s\r\nContext: from-internal\r\nAllow: (ulaw)\r\n\r\n",
				id, h["endpoint"])
			if h["endpoint"] == "wavecom" {
				// A trunk's contact is configuration: PJSIPShowContacts does not
				// report it, the endpoint does.
				detail += fmt.Sprintf("Event: AorDetail\r\nActionID: %s\r\nObjectName: wavecom\r\n"+
					"Contacts: wavecom/sip:voip.wavecom.ee\r\n\r\n"+
					"Event: ContactStatusDetail\r\nActionID: %s\r\nAOR: wavecom\r\nURI: sip:voip.wavecom.ee\r\n"+
					"EndpointName: wavecom\r\nStatus: Reachable\r\nRoundtripUsec: 31000\r\n\r\n", id, id)
			}
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\n\r\n%sEvent: EndpointDetailComplete\r\nActionID: %s\r\n\r\n",
				id, detail, id)
		case "pjsipshowcontacts":
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\n\r\n"+
				"Event: ContactList\r\nActionID: %s\r\nObjectType: contact\r\n"+
				"ObjectName: 1000;@a04f95567c2d51f59c93f480197e75af\r\nEndpoint: 1000\r\n"+
				"Uri: sip:1000@10.0.0.44:5060\r\nStatus: Reachable\r\nRoundtripUsec: 4200\r\n\r\n"+
				"Event: ContactListComplete\r\nActionID: %s\r\n\r\n", id, id, id)
		case "pjsipshowregistrationsoutbound":
			// FreePBX names the registration after the trunk ("wavecom-reg-0")
			// and leaves Endpoint unset; the contact stays under "wavecom".
			_, _ = fmt.Fprintf(c, "Response: Success\r\nActionID: %s\r\n\r\n"+
				"Event: OutboundRegistrationDetail\r\nActionID: %s\r\nObjectType: registration\r\n"+
				"ObjectName: wavecom-reg-0\r\nStatus: Registered\r\n"+
				"ServerUri: sip:sip.example.com\r\nClientUri: sip:user@example.com\r\n\r\n"+
				"Event: OutboundRegistrationDetailComplete\r\nActionID: %s\r\n\r\n", id, id, id)
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
	if len(d.Endpoints) != 2 {
		t.Errorf("endpoints = %d; want 2", len(d.Endpoints))
	}
	if len(d.Contacts) != 1 || d.Contacts[0].Roundtrip != "4.200 ms" {
		t.Errorf("contacts = %+v; want one contact with roundtrip 4.200 ms", d.Contacts)
	}
}

// An OutboundRegistrationDetail has no RTT of its own, and PJSIPShowContacts
// does not report a trunk's configured contact at all, so the trunk row must
// take the qualify time from its endpoint's ContactStatusDetail.
func TestRefreshTrunkRoundtripComesFromEndpointContactStatus(t *testing.T) {
	srv := startTestAMI(t)
	c := dialTestAMI(t, srv)
	defer c.Close()
	s := NewAsteriskService(c)

	if err := s.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	trunks := s.GetData().Trunks
	if len(trunks) != 1 {
		t.Fatalf("trunks = %d; want 1", len(trunks))
	}
	if trunks[0].Name != "wavecom-reg-0" {
		t.Errorf("trunk name = %q; want wavecom-reg-0", trunks[0].Name)
	}
	if trunks[0].Roundtrip != "31.000 ms" {
		t.Errorf("trunk roundtrip = %q; want 31.000 ms", trunks[0].Roundtrip)
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
