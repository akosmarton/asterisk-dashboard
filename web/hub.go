package web

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins
	},
}

// Hub maintains active WebSocket client connections and broadcasts data
type Hub struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan []byte
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mu         sync.Mutex
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan []byte, 100),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case conn := <-h.register:
			h.mu.Lock()
			h.clients[conn] = true
			h.mu.Unlock()

		case conn := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[conn]; ok {
				delete(h.clients, conn)
				_ = conn.Close()
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.Lock()
			for conn := range h.clients {
				_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
				err := conn.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					_ = conn.Close()
					delete(h.clients, conn)
				}
			}
			h.mu.Unlock()
		}
	}
}

// Broadcast sends JSON payload to all connected WebSocket clients
func (h *Hub) Broadcast(data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		log.Printf("[WebSocket] JSON marshal error: %v", err)
		return
	}
	h.broadcast <- b
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request, onConnect func() []byte) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WebSocket] Upgrade error: %v", err)
		return
	}

	h.register <- conn

	// Send initial state immediately upon connection
	if onConnect != nil {
		if initialData := onConnect(); initialData != nil {
			_ = conn.WriteMessage(websocket.TextMessage, initialData)
		}
	}

	// Keep-alive and read drain loop
	go func() {
		defer func() {
			h.unregister <- conn
		}()
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}
