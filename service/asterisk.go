package service

import (
	"fmt"
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

	epConfigCache map[string]struct{ context, allow string }
	tracker       *CallTracker
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

func (s *AsteriskService) Refresh() error {
	data := DashboardData{
		Global: GlobalState{
			LastUpdated:    time.Now().Format("2006-01-02 15:04:05"),
			Status:         "Online",
			ActiveCalls:    "0",
			ActiveChannels: "0",
			CallsProcessed: "0",
			Uptime:         "N/A",
			Version:        "Asterisk 23",
		},
		ActiveChannels: make([]ActiveChannelInfo, 0),
		Endpoints:      make([]EndpointInfo, 0),
		Contacts:       make([]ContactInfo, 0),
		Trunks:         make([]TrunkInfo, 0),
		CallHistory:    s.tracker.GetHistory(),
	}

	// 1. CoreStatus
	coreStatus, err := s.amiClient.SendAction("CoreStatus", nil)
	if err == nil && coreStatus != nil {
		if v, ok := coreStatus.Headers["corecurrentcalls"]; ok {
			data.Global.ActiveCalls = v
		}
		if v, ok := coreStatus.Headers["coreprocessedcalls"]; ok {
			data.Global.CallsProcessed = v
		}
		if v, ok := coreStatus.Headers["corereloadtime"]; ok {
			data.Global.ReloadTime = v
		}
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
	for _, ev := range epEvents {
		if strings.EqualFold(ev.Headers["event"], "EndpointList") {
			name := ev.Headers["objectname"]
			if name == "" {
				continue
			}

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

	// 6. Fetch Contacts via "pjsip show contacts" CLI
	contactLines, _ := s.amiClient.Command("pjsip show contacts")
	for _, l := range contactLines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "Contact:") {
			if strings.Contains(l, "<Aor/ContactUri") {
				continue
			}
			fields := strings.Fields(l)
			if len(fields) >= 4 {
				c := ContactInfo{
					ObjectName: fields[1],
					Status:     fields[3],
				}
				if len(fields) >= 5 {
					c.Roundtrip = fields[4] + " ms"
				}
				data.Contacts = append(data.Contacts, c)
			}
		}
	}

	// 7. Fetch Outbound Registrations (Trunks)
	regEvents, _ := s.amiClient.SendActionWithEvents("PJSIPShowRegistrationsOutbound", nil, "OutboundRegistrationDetailComplete", "RegistrationsOutboundListComplete")
	for _, ev := range regEvents {
		if strings.EqualFold(ev.Headers["event"], "OutboundRegistrationDetail") {
			data.Trunks = append(data.Trunks, TrunkInfo{
				Name:    ev.Headers["objectname"],
				Type:    "PJSIP Outbound",
				Status:  ev.Headers["status"],
				Address: ev.Headers["serveruri"],
				Details: fmt.Sprintf("Client: %s", ev.Headers["clienturi"]),
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
					Name:    ep.ObjectName,
					Type:    "SIP Trunk Endpoint",
					Status:  ep.Devicestate,
					Address: ep.Aor,
					Details: fmt.Sprintf("Channels: %s", ep.ActiveChannels),
				})
			}
		}
	}

	s.mu.Lock()
	s.lastData = data
	s.mu.Unlock()

	return nil
}

func (s *AsteriskService) getEndpointConfig(name string) (string, string) {
	if cfg, ok := s.epConfigCache[name]; ok && cfg.context != "" {
		return cfg.context, cfg.allow
	}

	detailEvents, err := s.amiClient.SendActionWithEvents("PJSIPShowEndpoint", map[string]string{"Endpoint": name}, "EndpointDetailComplete")
	if err == nil {
		for _, dev := range detailEvents {
			if strings.EqualFold(dev.Headers["event"], "EndpointDetail") {
				ctx := dev.Headers["context"]
				allow := dev.Headers["allow"]
				allow = strings.Trim(allow, "()")
				s.epConfigCache[name] = struct{ context, allow string }{context: ctx, allow: allow}
				return ctx, allow
			}
		}
	}

	return "-", "-"
}
