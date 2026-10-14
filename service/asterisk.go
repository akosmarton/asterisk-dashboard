package service

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"asterisk-dashboard/ami"
)

type ActiveChannelInfo struct {
	Channel          string `json:"channel"`
	ChannelStateDesc string `json:"channel_state_desc"`
	CallerIDNum      string `json:"caller_id_num"`
	CallerIDName     string `json:"caller_id_name"`
	ConnectedLineNum string `json:"connected_line_num"`
	Context          string `json:"context"`
	Extension        string `json:"extension"`
	Duration         string `json:"duration"`
	Application      string `json:"application"`
	AppData          string `json:"app_data"`
	AccountCode      string `json:"account_code"`
	BridgeID         string `json:"bridge_id"`
}

type EndpointInfo struct {
	ObjectName     string `json:"object_name"`
	Devicestate    string `json:"device_state"`
	ActiveChannels string `json:"active_channels"`
	Context        string `json:"context"`
	Allow          string `json:"allow"`
	Aor            string `json:"aor"`
}

type ContactInfo struct {
	ObjectName string `json:"object_name"`
	URI        string `json:"uri"`
	Status     string `json:"status"`
	Roundtrip  string `json:"roundtrip"`
}

type TrunkInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Address string `json:"address"`
	Details string `json:"details"`
	// Roundtrip is the qualify round-trip time of the contact belonging to the
	// trunk's endpoint. OutboundRegistrationDetail has no RTT of its own, so
	// this is read from the matching ContactList event.
	Roundtrip string `json:"roundtrip"`
}

type GlobalState struct {
	Version        string `json:"version"`
	Uptime         string `json:"uptime"`
	ReloadTime     string `json:"reload_time"`
	ActiveCalls    string `json:"active_calls"`
	ActiveChannels string `json:"active_channels"`
	CallsProcessed string `json:"calls_processed"`
	LastUpdated    string `json:"last_updated"`
	Status         string `json:"status"`
}

type DashboardData struct {
	Global         GlobalState         `json:"global"`
	ActiveChannels []ActiveChannelInfo `json:"active_channels_list"`
	Endpoints      []EndpointInfo      `json:"endpoints"`
	Contacts       []ContactInfo       `json:"contacts"`
	Trunks         []TrunkInfo         `json:"trunks"`
	CallHistory    []CallHistoryEntry  `json:"call_history"`
}

type AsteriskService struct {
	amiClient *ami.Client
	mu        sync.RWMutex
	lastData  DashboardData

	// epConfigCache is read and written by getEndpointConfig, which Refresh
	// calls from two goroutines at once (the 2s command loop and the AMI event
	// handler in main.go). It needs its own lock: s.mu only guards lastData.
	epConfigCache map[string]struct{ context, allow string }
	epCacheMu     sync.Mutex

	tracker *CallTracker
}

func NewAsteriskService(client *ami.Client) *AsteriskService {
	return &AsteriskService{
		amiClient:     client,
		epConfigCache: make(map[string]struct{ context, allow string }),
		tracker:       NewCallTracker(),
	}
}

func (s *AsteriskService) GetTracker() *CallTracker {
	return s.tracker
}

func (s *AsteriskService) GetData() DashboardData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastData
}

// Refresh rebuilds the dashboard snapshot from AMI. CoreStatus is the liveness
// probe: if it fails, AMI is unreachable, so the last good snapshot is
// republished with Status "Offline" — its version, uptime, counters and lists
// preserved and its LastUpdated left untouched, so "last updated" stops
// advancing — and a non-nil error is returned. Callers must broadcast
// GetData() even on error, so the UI shows the offline state instead of
// keeping a stale "Online".
func (s *AsteriskService) Refresh() error {
	coreStatus, err := s.amiClient.SendAction("CoreStatus", nil)
	if err != nil || coreStatus == nil {
		if err == nil {
			err = fmt.Errorf("empty CoreStatus response")
		}
		s.mu.Lock()
		stale := s.lastData
		stale.Global.Status = "Offline"
		s.lastData = stale
		s.mu.Unlock()
		return fmt.Errorf("AMI unreachable: %w", err)
	}

	// Fresh snapshot. Identity fields that a single AMI call may not have
	// answered yet start from the last known values rather than fabrications
	// like a hardcoded version.
	prev := s.GetData()
	data := DashboardData{
		Global: GlobalState{
			LastUpdated:    time.Now().Format("2006-01-02 15:04:05"),
			Status:         "Online",
			ActiveCalls:    "0",
			ActiveChannels: "0",
			CallsProcessed: "0",
			Uptime:         prev.Global.Uptime,
			Version:        prev.Global.Version,
			ReloadTime:     prev.Global.ReloadTime,
		},
		ActiveChannels: make([]ActiveChannelInfo, 0),
		Endpoints:      make([]EndpointInfo, 0),
		Contacts:       make([]ContactInfo, 0),
		Trunks:         make([]TrunkInfo, 0),
		CallHistory:    s.tracker.GetHistory(),
	}

	// 1. CoreStatus (already fetched above as the liveness probe)
	if v, ok := coreStatus.Headers["corecurrentcalls"]; ok {
		data.Global.ActiveCalls = v
	}
	if v, ok := coreStatus.Headers["coreprocessedcalls"]; ok {
		data.Global.CallsProcessed = v
	}
	if v, ok := coreStatus.Headers["corereloadtime"]; ok {
		data.Global.ReloadTime = v
	}

	// 2. CoreSettings for exact Asterisk Version
	coreSettings, err := s.amiClient.SendAction("CoreSettings", nil)
	if err == nil && coreSettings != nil {
		if ver, ok := coreSettings.Headers["asteriskversion"]; ok && ver != "" {
			data.Global.Version = fmt.Sprintf("Asterisk %s", ver)
		}
	}

	// 3. System Uptime & Channel count
	uptimeLines, _ := s.amiClient.Command("core show uptime")
	for _, l := range uptimeLines {
		if strings.HasPrefix(l, "System uptime:") {
			rawUptime := strings.TrimSpace(strings.TrimPrefix(l, "System uptime:"))
			data.Global.Uptime = FormatShortUptime(rawUptime)
			break
		}
	}

	chanLines, _ := s.amiClient.Command("core show channels count")
	for _, l := range chanLines {
		lower := strings.ToLower(l)
		if strings.Contains(lower, "active channel") {
			fields := strings.Fields(l)
			if len(fields) > 0 {
				data.Global.ActiveChannels = fields[0]
			}
		} else if strings.Contains(lower, "active call") {
			fields := strings.Fields(l)
			if len(fields) > 0 {
				data.Global.ActiveCalls = fields[0]
			}
		} else if strings.Contains(lower, "calls processed") {
			fields := strings.Fields(l)
			if len(fields) > 0 {
				data.Global.CallsProcessed = fields[0]
			}
		}
	}

	// 4. Fetch Active Calls / Channels via CoreShowChannels
	chanEvents, _ := s.amiClient.SendActionWithEvents("CoreShowChannels", nil, "CoreShowChannelsComplete")
	for _, ev := range chanEvents {
		if strings.EqualFold(ev.Headers["event"], "CoreShowChannel") {
			callerNum := ev.Headers["calleridnum"]
			callerName := ev.Headers["calleridname"]
			connectedNum := ev.Headers["connectedlinenum"]
			if connectedNum == "<unknown>" {
				connectedNum = "-"
			}
			if callerNum == "<unknown>" {
				callerNum = "-"
			}

			data.ActiveChannels = append(data.ActiveChannels, ActiveChannelInfo{
				Channel:          ev.Headers["channel"],
				ChannelStateDesc: ev.Headers["channelstatedesc"],
				CallerIDNum:      callerNum,
				CallerIDName:     callerName,
				ConnectedLineNum: connectedNum,
				Context:          ev.Headers["context"],
				Extension:        ev.Headers["exten"],
				Duration:         ev.Headers["duration"],
				Application:      ev.Headers["application"],
				AppData:          ev.Headers["data"],
				AccountCode:      ev.Headers["accountcode"],
				BridgeID:         ev.Headers["bridgeid"],
			})
		}
	}

	// 5. Fetch PJSIP Endpoints list
	epEvents, _ := s.amiClient.SendActionWithEvents("PJSIPShowEndpoints", nil, "EndpointListComplete")
	// Base name -> endpoint name, so a FreePBX outbound registration
	// ("wavecom-reg-0") can be traced back to the endpoint that owns the trunk's
	// contact.
	endpointNames := make(map[string]string)
	for _, ev := range epEvents {
		if strings.EqualFold(ev.Headers["event"], "EndpointList") {
			name := ev.Headers["objectname"]
			if name == "" {
				continue
			}
			endpointNames[trunkBaseName(name)] = name

			ctx, allow := s.getEndpointConfig(name)

			data.Endpoints = append(data.Endpoints, EndpointInfo{
				ObjectName:     name,
				Devicestate:    ev.Headers["devicestate"],
				ActiveChannels: ev.Headers["activechannels"],
				Context:        ctx,
				Allow:          allow,
				Aor:            ev.Headers["aor"],
			})
		}
	}

	// 6. Fetch PJSIP Contacts. Not via the "pjsip show contacts" CLI: that
	// prints a fixed-width table and cuts the Contact column at ~46 characters,
	// which truncates the IPv6 URIs mid-address. The action's ContactList
	// events carry the full Uri plus the endpoint the contact belongs to.
	contactEvents, _ := s.amiClient.SendActionWithEvents("PJSIPShowContacts", nil, "ContactListComplete")
	// Base name -> qualify round-trip, so a trunk row can show the latency of its
	// contact when that contact comes back from this action. It does not for a
	// trunk: PJSIPShowContacts only reports the contacts that exist as sorcery
	// objects, i.e. the dynamic ones created by an inbound REGISTER.
	// trunkEndpointRTT covers the configured contact a trunk actually has.
	contactRTT := make(map[string]string)
	for _, ev := range contactEvents {
		uri := ev.Headers["uri"]
		if !strings.EqualFold(ev.Headers["event"], "ContactList") || uri == "" {
			continue
		}

		c := ContactInfo{
			ObjectName: ev.Headers["endpoint"] + "/" + uri,
			URI:        uri,
			Status:     ev.Headers["status"],
		}
		if usec, err := strconv.Atoi(ev.Headers["roundtripusec"]); err == nil {
			c.Roundtrip = fmt.Sprintf("%.3f ms", float64(usec)/1000)
		}
		data.Contacts = append(data.Contacts, c)

		if ep := trunkBaseName(ev.Headers["endpoint"]); ep != "" && c.Roundtrip != "" {
			if _, ok := contactRTT[ep]; !ok {
				contactRTT[ep] = c.Roundtrip
			}
		}
	}

	// resolveTrunkRTT returns the qualify round-trip for a trunk row, trying each
	// candidate object name in turn: the cheap contact table first, then the
	// trunk endpoint's own contact status.
	resolveTrunkRTT := func(candidates ...string) string {
		for _, candidate := range candidates {
			if rtt := contactRTT[trunkBaseName(candidate)]; rtt != "" {
				return rtt
			}
		}
		for _, candidate := range candidates {
			if endpoint, ok := endpointNames[trunkBaseName(candidate)]; ok {
				if rtt := s.trunkEndpointRTT(endpoint); rtt != "" {
					return rtt
				}
			}
		}
		return ""
	}

	// 7. Fetch Outbound Registrations (Trunks)
	regEvents, _ := s.amiClient.SendActionWithEvents("PJSIPShowRegistrationsOutbound", nil, "OutboundRegistrationDetailComplete", "RegistrationsOutboundListComplete")
	for _, ev := range regEvents {
		if strings.EqualFold(ev.Headers["event"], "OutboundRegistrationDetail") {
			name := ev.Headers["objectname"]
			// A registration has no latency of its own; its RTT is the trunk's,
			// i.e. the endpoint contact's. FreePBX names the registration after
			// the trunk ("wavecom-reg-0") and may leave Endpoint unset, so both
			// names are candidates.
			data.Trunks = append(data.Trunks, TrunkInfo{
				Name:      name,
				Type:      "PJSIP Outbound",
				Status:    ev.Headers["status"],
				Address:   ev.Headers["serveruri"],
				Details:   fmt.Sprintf("Client: %s", ev.Headers["clienturi"]),
				Roundtrip: resolveTrunkRTT(ev.Headers["endpoint"], name),
			})
		}
	}

	// Also add provider trunk endpoint if found
	for _, ep := range data.Endpoints {
		if ep.ObjectName == "wavecom" || strings.Contains(strings.ToLower(ep.ObjectName), "trunk") {
			found := false
			for _, t := range data.Trunks {
				if strings.Contains(t.Name, ep.ObjectName) {
					found = true
					break
				}
			}
			if !found {
				data.Trunks = append(data.Trunks, TrunkInfo{
					Name:      ep.ObjectName,
					Type:      "SIP Trunk Endpoint",
					Status:    ep.Devicestate,
					Address:   ep.Aor,
					Details:   fmt.Sprintf("Channels: %s", ep.ActiveChannels),
					Roundtrip: resolveTrunkRTT(ep.ObjectName),
				})
			}
		}
	}

	s.mu.Lock()
	s.lastData = data
	s.mu.Unlock()

	return nil
}

// trunkBaseName lowercases a PJSIP object name and strips FreePBX's
// per-registration suffix ("wavecom-reg-0" -> "wavecom"). A registered trunk
// keeps its endpoint, aor and contact under the bare trunk name, so a
// registration only lines up with them after this reduction.
func trunkBaseName(name string) string {
	name = strings.ToLower(name)
	if i := strings.LastIndex(name, "-reg-"); i > 0 {
		if _, err := strconv.Atoi(name[i+5:]); err == nil {
			return name[:i]
		}
	}
	return name
}

// trunkEndpointRTT returns the qualify round-trip time of a trunk endpoint's
// contact.
//
// The contact table cannot supply it: PJSIPShowContacts only reports the
// contacts that exist as sorcery objects, i.e. the dynamic ones an inbound
// REGISTER created. A trunk's contact is configuration — "pjsip show contacts"
// lists it with an RTT while that action reports nothing at all for it (checked
// against Asterisk 23.5). The endpoint's ContactStatusDetail does carry it, one
// event per contact of the endpoint's AORs.
func (s *AsteriskService) trunkEndpointRTT(endpoint string) string {
	events, err := s.amiClient.SendActionWithEvents("PJSIPShowEndpoint", map[string]string{"Endpoint": endpoint}, "EndpointDetailComplete")
	if err != nil {
		return ""
	}
	for _, ev := range events {
		if !strings.EqualFold(ev.Headers["event"], "ContactStatusDetail") {
			continue
		}
		// RoundtripUsec is "N/A" until the contact has qualified at least once.
		if usec, err := strconv.Atoi(ev.Headers["roundtripusec"]); err == nil {
			return fmt.Sprintf("%.3f ms", float64(usec)/1000)
		}
	}
	return ""
}

func (s *AsteriskService) getEndpointConfig(name string) (string, string) {
	s.epCacheMu.Lock()
	cfg, ok := s.epConfigCache[name]
	s.epCacheMu.Unlock()
	if ok && cfg.context != "" {
		return cfg.context, cfg.allow
	}

	detailEvents, err := s.amiClient.SendActionWithEvents("PJSIPShowEndpoint", map[string]string{"Endpoint": name}, "EndpointDetailComplete")
	if err == nil {
		for _, dev := range detailEvents {
			if strings.EqualFold(dev.Headers["event"], "EndpointDetail") {
				ctx := dev.Headers["context"]
				allow := dev.Headers["allow"]
				allow = strings.Trim(allow, "()")
				s.epCacheMu.Lock()
				s.epConfigCache[name] = struct{ context, allow string }{context: ctx, allow: allow}
				s.epCacheMu.Unlock()
				return ctx, allow
			}
		}
	}

	return "-", "-"
}
