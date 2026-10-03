package server

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/mai/pkg/interfaces"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 256 * 1024 // 256 KB — large enough for TTS audio chunks
)

// Client represents a single WebSocket connection.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	id   string
}

// Hub manages all connected WebSocket clients.
type Hub struct {
	clients       map[*Client]bool
	broadcast     chan []byte
	register      chan *Client
	unregister    chan *Client
	mu            sync.RWMutex
	eventBus      interfaces.EventBus
	getStatusFunc func() string
	onClientGone  func() // called after a client fully disconnects
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 512),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("[WS] Client connected: %s (total: %d)", client.id, len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			log.Printf("[WS] Client disconnected: %s (total: %d)", client.id, len(h.clients))
			if h.onClientGone != nil {
				h.onClientGone()
			}

		case message := <-h.broadcast:
			// Collect stale clients first, then remove them under write lock.
			h.mu.RLock()
			var stale []*Client
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					stale = append(stale, client)
				}
			}
			h.mu.RUnlock()

			if len(stale) > 0 {
				h.mu.Lock()
				for _, client := range stale {
					if _, ok := h.clients[client]; ok {
						delete(h.clients, client)
						close(client.send)
					}
				}
				h.mu.Unlock()
				if h.onClientGone != nil {
					h.onClientGone()
				}
			}
		}
	}
}

// broadcastDrops counts notifications shed since the last report, and
// broadcastLastReport is the UnixNano of that report. Drops are only worth
// saying out loud when they are sustained — the publishers that matter here
// (interim ASR transcripts) run at roughly ten per second, so a log line per
// drop would cost more than the drops themselves.
var (
	broadcastDrops       atomic.Int64
	broadcastLastReport  atomic.Int64
	broadcastReportEvery = 30 * time.Second
)

// reportBroadcastDrops logs the accumulated drop count at most once per
// broadcastReportEvery. It is called only on the drop path, which is already
// the slow path, so it stays lock-free.
func reportBroadcastDrops() {
	now := time.Now().UnixNano()
	last := broadcastLastReport.Load()
	if now-last < int64(broadcastReportEvery) || !broadcastLastReport.CompareAndSwap(last, now) {
		return
	}
	if n := broadcastDrops.Swap(0); n > 0 {
		log.Printf("[WS] Hub not draining: dropped %d notification(s) in the last %v", n, broadcastReportEvery)
	}
}

// BroadcastToAll sends a message to every connected client.
//
// The send is deliberately non-blocking, matching SendToClient above. The
// event bus dispatches subscribers on the *publisher's* goroutine, so a blocking
// send here would stall whatever produced the notification — for interim ASR
// transcripts that is the audio pipeline, where a stall drops microphone frames.
// Shedding a notification is always preferable to that: the next one supersedes
// it, and Run() above already evicts clients that cannot keep up.
func (h *Hub) BroadcastToAll(data []byte) {
	select {
	case h.broadcast <- data:
	default:
		broadcastDrops.Add(1)
		reportBroadcastDrops()
	}
}

// SendToClient sends a message to a specific client.
func (h *Hub) SendToClient(client *Client, data []byte) {
	select {
	case client.send <- data:
	default:
		log.Printf("[WS] Send buffer full for client %s, dropping message", client.id)
	}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// readPump pumps messages from the WebSocket connection to the hub.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[WS] Read error from %s: %v", c.id, err)
			}
			break
		}
		c.hub.HandleMessage(c, message)
	}
}

// writePump pumps messages from the hub to the WebSocket connection.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
