package service

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CallHistoryEntry struct {
	ID          string `json:"id"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Caller      string `json:"caller"`
	CallerName  string `json:"caller_name"`
	Destination string `json:"destination"`
	Context     string `json:"context"`
	Duration    string `json:"duration"`
	Billable    string `json:"billable"`
	Disposition string `json:"disposition"`
	Channel     string `json:"channel"`
	DstChannel  string `json:"dst_channel"`
}

type activeCall struct {
	uniqueID    string
	linkedID    string
	channel     string
	dstChannel  string
	caller      string
	callerName  string
	destination string
	context     string
	startTime   time.Time
	answerTime  time.Time
	isAnswered  bool
	isPrimary   bool
	disposition string
}

type CallTracker struct {
	mu          sync.RWMutex
	activeCalls map[string]*activeCall
	history     []CallHistoryEntry
	maxHistory  int
}

func NewCallTracker() *CallTracker {
	return &CallTracker{
		activeCalls: make(map[string]*activeCall),
		history:     make([]CallHistoryEntry, 0),
		maxHistory:  100,
	}
}

func formatDurationSecs(secs int) string {
	if secs < 0 {
		secs = 0
	}
	m := secs / 60
	s := secs % 60
	if m >= 60 {
		h := m / 60
		m = m % 60
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func (ct *CallTracker) GetHistory() []CallHistoryEntry {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	result := make([]CallHistoryEntry, len(ct.history))
	copy(result, ct.history)
	return result
}

func (ct *CallTracker) addOrUpdateHistory(entry CallHistoryEntry) {
	for i, h := range ct.history {
		if h.ID == entry.ID {
			if entry.StartTime != "" {
				ct.history[i].StartTime = entry.StartTime
			}
			if entry.EndTime != "" {
				ct.history[i].EndTime = entry.EndTime
			}
			if entry.Caller != "" && entry.Caller != "-" {
				ct.history[i].Caller = entry.Caller
			}
			if entry.CallerName != "" {
				ct.history[i].CallerName = entry.CallerName
			}
			if entry.Destination != "" && entry.Destination != "-" && entry.Destination != "s" {
				ct.history[i].Destination = entry.Destination
			}
			if entry.Context != "" {
				ct.history[i].Context = entry.Context
			}
			if entry.Duration != "" {
				ct.history[i].Duration = entry.Duration
			}
			if entry.Billable != "" {
				ct.history[i].Billable = entry.Billable
			}
			if entry.Disposition != "" {
				ct.history[i].Disposition = entry.Disposition
			}
			if entry.Channel != "" {
				ct.history[i].Channel = entry.Channel
			}
			if entry.DstChannel != "" {
				ct.history[i].DstChannel = entry.DstChannel
			}
			return
		}
	}

	ct.history = append([]CallHistoryEntry{entry}, ct.history...)
	if len(ct.history) > ct.maxHistory {
		ct.history = ct.history[:ct.maxHistory]
	}
}

// HandleEvent processes an AMI event and returns true if call history was modified
func (ct *CallTracker) HandleEvent(eventName string, headers map[string]string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ev := strings.ToLower(eventName)

	switch ev {
	case "cdr":
		uniqueID := headers["uniqueid"]
		if uniqueID == "" {
			return false
		}

		durSec, _ := strconv.Atoi(headers["duration"])
		billSec, _ := strconv.Atoi(headers["billableseconds"])

		caller := headers["source"]
		if caller == "<unknown>" || caller == "" {
			caller = headers["calleridnum"]
		}
		if caller == "<unknown>" || caller == "" {
			caller = "-"
		}

		callerName := headers["calleridname"]
		dst := headers["destination"]
		if dst == "" || dst == "<unknown>" {
			dst = headers["exten"]
		}

		disp := strings.ToUpper(headers["disposition"])
		if disp == "" {
			disp = "ANSWERED"
		}

		entry := CallHistoryEntry{
			ID:          uniqueID,
			StartTime:   headers["starttime"],
			EndTime:     headers["endtime"],
			Caller:      caller,
			CallerName:  callerName,
			Destination: dst,
			Context:     headers["destinationcontext"],
			Duration:    formatDurationSecs(durSec),
			Billable:    formatDurationSecs(billSec),
			Disposition: disp,
			Channel:     headers["channel"],
			DstChannel:  headers["destinationchannel"],
		}

		delete(ct.activeCalls, uniqueID)
		ct.addOrUpdateHistory(entry)
		return true

	case "newchannel":
		uniqueID := headers["uniqueid"]
		if uniqueID == "" {
			return false
		}
		linkedID := headers["linkedid"]
		caller := headers["calleridnum"]
		if caller == "<unknown>" || caller == "" {
			caller = "-"
		}
		callerName := headers["calleridname"]
		exten := headers["exten"]
		ctx := headers["context"]
		channel := headers["channel"]

		isPrimary := (linkedID == "" || linkedID == uniqueID)

		ct.activeCalls[uniqueID] = &activeCall{
			uniqueID:    uniqueID,
			linkedID:    linkedID,
			channel:     channel,
			caller:      caller,
			callerName:  callerName,
			destination: exten,
			context:     ctx,
			startTime:   time.Now(),
			isPrimary:   isPrimary,
		}
		return false

	case "newstate":
		uniqueID := headers["uniqueid"]
		if call, ok := ct.activeCalls[uniqueID]; ok {
			stateDesc := strings.ToLower(headers["channelstatedesc"])
			if stateDesc == "up" {
				call.isAnswered = true
				if call.answerTime.IsZero() {
					call.answerTime = time.Now()
				}
				call.disposition = "ANSWERED"
			}
			if connNum := headers["connectedlinenum"]; connNum != "" && connNum != "<unknown>" && (call.destination == "" || call.destination == "s") {
				call.destination = connNum
			}
		}
		return false

	case "dialbegin":
		uniqueID := headers["uniqueid"]
		if call, ok := ct.activeCalls[uniqueID]; ok {
			if call.dstChannel == "" {
				call.dstChannel = headers["destchannel"]
			}
			dialStr := headers["dialstring"]
			if dialStr != "" && (call.destination == "" || call.destination == "s") {
				call.destination = dialStr
			}
		}
		return false

	case "dialend":
		uniqueID := headers["uniqueid"]
		if call, ok := ct.activeCalls[uniqueID]; ok {
			status := strings.ToUpper(headers["dialstatus"])
			if status == "ANSWER" {
				call.isAnswered = true
				if call.answerTime.IsZero() {
					call.answerTime = time.Now()
				}
				call.disposition = "ANSWERED"
			} else if !call.isAnswered {
				call.disposition = status
			}
		}
		return false

	case "bridgeenter":
		uniqueID := headers["uniqueid"]
		if call, ok := ct.activeCalls[uniqueID]; ok {
			call.isAnswered = true
			if call.answerTime.IsZero() {
				call.answerTime = time.Now()
			}
			call.disposition = "ANSWERED"
		}
		return false

	case "hangup":
		uniqueID := headers["uniqueid"]
		call, ok := ct.activeCalls[uniqueID]
		if !ok {
			return false
		}
		delete(ct.activeCalls, uniqueID)

		if !call.isPrimary {
			return false
		}

		now := time.Now()
		totalSecs := int(now.Sub(call.startTime).Seconds())
		billSecs := 0
		if call.isAnswered && !call.answerTime.IsZero() {
			billSecs = int(now.Sub(call.answerTime).Seconds())
		}

		disp := call.disposition
		if disp == "" {
			causeTxt := strings.ToUpper(headers["cause-txt"])
			if call.isAnswered {
				disp = "ANSWERED"
			} else if strings.Contains(causeTxt, "BUSY") {
				disp = "BUSY"
			} else if strings.Contains(causeTxt, "REJECT") || strings.Contains(causeTxt, "CONGESTION") {
				disp = "FAILED"
			} else if strings.Contains(causeTxt, "NO ANSWER") {
				disp = "NO ANSWER"
			} else {
				disp = "CANCELLED"
			}
		}

		dst := call.destination
		if dst == "" || dst == "s" {
			if exten := headers["exten"]; exten != "" && exten != "s" {
				dst = exten
			} else if conn := headers["connectedlinenum"]; conn != "" && conn != "<unknown>" {
				dst = conn
			} else {
				dst = "-"
			}
		}

		entry := CallHistoryEntry{
			ID:          call.uniqueID,
			StartTime:   call.startTime.Format("2006-01-02 15:04:05"),
			EndTime:     now.Format("2006-01-02 15:04:05"),
			Caller:      call.caller,
			CallerName:  call.callerName,
			Destination: dst,
			Context:     call.context,
			Duration:    formatDurationSecs(totalSecs),
			Billable:    formatDurationSecs(billSecs),
			Disposition: disp,
			Channel:     call.channel,
			DstChannel:  call.dstChannel,
		}

		ct.addOrUpdateHistory(entry)
		return true
	}

	return false
}
