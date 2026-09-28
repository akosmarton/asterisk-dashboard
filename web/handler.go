package web

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"asterisk-dashboard/service"
)

//go:embed page.html
var pageHTML string

type Server struct {
	service *service.AsteriskService
	hub     *Hub
	tmpl    *template.Template
}

func NewServer(srv *service.AsteriskService, hub *Hub) *Server {
	funcMap := template.FuncMap{
		"getDeviceBadge": func(state string) string {
			s := strings.ToLower(state)
			if strings.Contains(s, "not in use") || strings.Contains(s, "idle") || strings.Contains(s, "up") {
				return "badge-green"
			}
			if strings.Contains(s, "in use") || strings.Contains(s, "busy") || strings.Contains(s, "ringing") || strings.Contains(s, "ring") {
				return "badge-yellow"
			}
			if strings.Contains(s, "unavailable") || strings.Contains(s, "unavail") || strings.Contains(s, "down") {
				return "badge-red"
			}
			return "badge-gray"
		},
		"getStatusBadge": func(status string) string {
			s := strings.ToLower(status)
			if strings.Contains(s, "reachable") || strings.Contains(s, "registered") || strings.Contains(s, "avail") {
				return "badge-green"
			}
			if strings.Contains(s, "unreachable") || strings.Contains(s, "unregistered") || strings.Contains(s, "rejected") {
				return "badge-red"
			}
			return "badge-gray"
		},
		"getDispositionBadge": func(disp string) string {
			s := strings.ToUpper(disp)
			if strings.Contains(s, "ANSWER") {
				return "badge-green"
			}
			if strings.Contains(s, "BUSY") {
				return "badge-yellow"
			}
			if strings.Contains(s, "NO ANSWER") || strings.Contains(s, "CANCEL") {
				return "badge-gray"
			}
			if strings.Contains(s, "FAIL") || strings.Contains(s, "REJECT") || strings.Contains(s, "CONGESTION") {
				return "badge-red"
			}
			return "badge-gray"
		},
	}

	tmpl := template.Must(template.New("dashboard").Funcs(funcMap).Parse(pageHTML))

	return &Server{
		service: srv,
		hub:     hub,
		tmpl:    tmpl,
	}
}

func (s *Server) HandleIndex(w http.ResponseWriter, r *http.Request) {
	data := s.service.GetData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.Execute(w, data)
}

func (s *Server) HandleData(w http.ResponseWriter, r *http.Request) {
	data := s.service.GetData()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	s.hub.HandleWS(w, r, func() []byte {
		data := s.service.GetData()
		b, _ := json.Marshal(data)
		return b
	})
}
