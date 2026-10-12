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

const (
	// outboundQueueSize is the per-client outbound message buffer. Every queued
	// message is written by that client's single writer goroutine, so a client
	// that stops reading can never make another goroutine write to (or block on)
	// its connection.
	outboundQueueSize = 16

	// controlQueueSize buffers hub control messages (register/unregister) so
	// HandleWS and the per-client read/write goroutines never block waiting for
	// Run to become available.
	controlQueueSize = 128

	// writeTimeout bounds a single write to one client. A client that does not
	// drain its socket within this window is dropped.
	writeTimeout = 3 * time.Second
)

// client is one WebSocket subscriber. Exactly one goroutine (its writeLoop)
// ever writes to conn. done is closed once, by close(), to stop both loops.
type client struct {
	conn *websocket.Conn
	send chan []byte
	done chan struct{}
	once sync.Once
}

// close stops the client's loops and closes the underlying connection. Safe to
// call from any goroutine and any number of times.
func (c *client) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// trySend enqueues msg without ever blocking the caller.
//
// Slow-client policy: the queue is a bounded buffer of complete dashboard
// snapshots, so if it is full the oldest queued snapshot is discarded in favour
// of the newest one; if even that fails (the writer is draining concurrently)
// the update is dropped. The client is not disconnected here — the writer's
// per-write deadline (writeTimeout) closes clients that stop draining entirely.
func (c *client) trySend(msg []byte) {
	select {
	case <-c.done:
		return
	case c.send <- msg:
		return
	default:
	}
	// Queue full: drop the oldest snapshot, then enqueue the newest.
	select {
	case <-c.send:
	default:
	}
	select {
	case c.send <- msg:
	case <-c.done:
	default:
	}
}

// Hub maintains active WebSocket client connections and broadcasts data.
type Hub struct {
	register   chan *client
	unregister chan *client

	clients map[*client]bool
	mu      sync.Mutex
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*client]bool),
		register:   make(chan *client, controlQueueSize),
		unregister: make(chan *client, controlQueueSize),
	}
}

// Run owns the client set. It never writes to a connection itself and every
// channel it touches is buffered, so it cannot be stalled by a slow client.
func (h *Hub) Run() {
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[WebSocket] recovered panic in hub run loop: %v", r)
				}
			}()
			select {
			case c := <-h.register:
				h.mu.Lock()
				h.clients[c] = true
				h.mu.Unlock()

			case c := <-h.unregister:
				h.mu.Lock()
				if _, ok := h.clients[c]; ok {
					delete(h.clients, c)
					h.mu.Unlock()
					c.close()
					return
				}
				h.mu.Unlock()
			}
		}()
	}
}

// Broadcast sends JSON payload to all connected WebSocket clients. It only ever
// enqueues into bounded per-client buffers, so it never blocks its caller —
// even if some clients have stopped reading. Payloads are complete snapshots,
// so a dropped/discarded update is superseded by the next one.
func (h *Hub) Broadcast(data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		log.Printf("[WebSocket] JSON marshal error: %v", err)
		return
	}

	h.mu.Lock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		c.trySend(b)
	}
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request, onConnect func() []byte) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WebSocket] Upgrade error: %v", err)
		return
	}

	c := &client{
		conn: conn,
		send: make(chan []byte, outboundQueueSize),
		done: make(chan struct{}),
	}

	// Queue the initial snapshot as the first message; the writer goroutine
	// (the only writer to conn) delivers it.
	if onConnect != nil {
		if initialData := onConnect(); initialData != nil {
			c.trySend(initialData)
		}
	}

	go h.readLoop(c)
	go h.writeLoop(c)

	h.register <- c
}

// readLoop drains inbound frames until the connection fails, then tears the
// client down. It never writes to the connection.
func (h *Hub) readLoop(c *client) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[WebSocket] recovered panic in read loop: %v", r)
		}
		c.close()
		h.unregister <- c
	}()
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// writeLoop is the single goroutine that writes to c.conn. It exits when the
// client is closed or a write fails / exceeds writeTimeout.
func (h *Hub) writeLoop(c *client) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[WebSocket] recovered panic in write loop: %v", r)
		}
		c.close()
		h.unregister <- c
	}()
	for {
		select {
		case <-c.done:
			return
		case msg := <-c.send:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("[WebSocket] write error: %v", err)
				return
			}
		}
	}
}
