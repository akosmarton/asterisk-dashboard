package web

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"asterisk-dashboard/service"
)

type Server struct {
	service *service.AsteriskService
	hub     *Hub
	index   []byte
	favicon []byte
	static  http.Handler
}

// NewServer builds the dashboard server. assets serves the frontend build: the
// embedded ui/dist in production (see main.go), or os.DirFS("ui/dist") with
// -dev. It must contain index.html, which main checks before getting here.
func NewServer(srv *service.AsteriskService, hub *Hub, assets fs.FS) *Server {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		panic("web: frontend build has no index.html: " + err.Error())
	}
	// The icon is optional: a build without it is still usable.
	favicon, _ := fs.ReadFile(assets, "favicon.svg")

	return &Server{
		service: srv,
		hub:     hub,
		index:   index,
		favicon: favicon,
		static:  http.FileServerFS(assets),
	}
}

// HandleIndex serves the SPA shell for every non-API path — including the ones
// the client routes itself. The shell is not gated: the app asks /api/session
// and renders the login view when there is no session.
func (s *Server) HandleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(s.index)
}

func (s *Server) HandleData(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.service.GetData())
}

func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	s.hub.HandleWS(w, r, func() []byte {
		data := s.service.GetData()
		b, _ := json.Marshal(data)
		return b
	})
}

// HandleFavicon serves the build's favicon.svg at /favicon.svg and
// /favicon.ico. The path is fixed, so it revalidates on every request like the
// rest of the bundle: a redrawn icon would otherwise sit in the browser cache
// for as long as the max-age says.
func (s *Server) HandleFavicon(w http.ResponseWriter, r *http.Request) {
	if len(s.favicon) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(s.favicon)
}

// StaticHandler serves the frontend build. Mount it with
// http.StripPrefix("/static/", …). The file names are fixed (app.js, app.css),
// so a rebuild reuses them — revalidate on every request instead of caching.
func (s *Server) StaticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		s.static.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
