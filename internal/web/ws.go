package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingyoung/bbsit/internal/deployer"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ClientMessage is a message sent from the browser to the hub.
type ClientMessage struct {
	Action     string   `json:"action"`      // "subscribe" or "unsubscribe"
	ProjectIDs []string `json:"project_ids"`
}

// client represents a single WebSocket connection.
type client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte

	mu       sync.RWMutex
	projects map[string]bool // subscribed project IDs
}

// Hub manages WebSocket clients and broadcasts deployer events.
type Hub struct {
	clients    map[*client]bool
	register   chan *client
	unregister chan *client
	broadcast  chan []byte
	event      chan deployer.Event
	stop       chan struct{}
	mu         sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*client]bool),
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan []byte, 256),
		event:      make(chan deployer.Event, 256),
		stop:       make(chan struct{}),
	}
}

// OnEvent implements deployer.DeployListener.
func (h *Hub) OnEvent(e deployer.Event) {
	select {
	case h.event <- e:
	default:
		// Drop event if buffer full (don't block deployer)
	}
}

func (h *Hub) Run() {
	for {
		select {
		case <-h.stop:
			h.mu.Lock()
			for c := range h.clients {
				close(c.send)
				delete(h.clients, c)
			}
			h.mu.Unlock()
			return
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = true
			h.mu.Unlock()
		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				close(c.send)
				delete(h.clients, c)
			}
			h.mu.Unlock()
		case e := <-h.event:
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			h.mu.RLock()
			for c := range h.clients {
				if c.isSubscribed(e.ProjectID) {
					select {
					case c.send <- data:
					default:
						go func(cl *client) {
							h.unregister <- cl
						}(c)
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) Stop() {
	close(h.stop)
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("ws upgrade", "error", err)
		return
	}

	c := &client{
		hub:      h,
		conn:     conn,
		send:     make(chan []byte, 64),
		projects: make(map[string]bool),
	}

	h.register <- c

	go c.writePump()
	go c.readPump()
}

func (c *client) isSubscribed(projectID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.projects[projectID]
}

func (c *client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		var cm ClientMessage
		if json.Unmarshal(msg, &cm) != nil {
			continue
		}

		c.mu.Lock()
		switch cm.Action {
		case "subscribe":
			for _, id := range cm.ProjectIDs {
				c.projects[id] = true
			}
		case "unsubscribe":
			for _, id := range cm.ProjectIDs {
				delete(c.projects, id)
			}
		}
		c.mu.Unlock()
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
