package ws

import (
	"encoding/json"
	"sync"

	"github.com/coder/websocket"
)

type Msg struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`
}

type Client struct {
	Conn *websocket.Conn
	Send chan []byte
}

type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]bool
}

func NewHub() *Hub {
	return &Hub{clients: make(map[*Client]bool)}
}

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func (h *Hub) Broadcast(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.Send <- data:
		default:
		}
	}
}

func (h *Hub) BroadcastJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.Broadcast(data)
}
