package server

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/user/mai/pkg/interfaces"
)

// ServerConfig holds the WS/HTTP server configuration.
type ServerConfig struct {
	Enabled bool   `yaml:"enabled"`
	Port    int    `yaml:"port"`
	Token   string `yaml:"token"`
}

// Server is the Mai companion WebSocket + HTTP server.
type Server struct {
	cfg        ServerConfig
	hub        *Hub
	upgrader   websocket.Upgrader
	eventBus   interfaces.EventBus
	isSpeaking *int32
	ttsPlaying *int32
 getStatus  func() string
}

// New creates a new Server.
func New(cfg ServerConfig, bus interfaces.EventBus, isSpeaking, ttsPlaying *int32, getStatus func() string) *Server {
	return &Server{
		cfg:        cfg,
		hub:        NewHub(),
		eventBus:   bus,
		isSpeaking: isSpeaking,
		ttsPlaying: ttsPlaying,
		getStatus:  getStatus,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// Start begins the HTTP server and hub. Non-blocking.
func (s *Server) Start() error {
	go s.hub.Run()

	// Start the event bus bridge
	NewBridge(s.eventBus, s.hub, func() interfaces.AgentStatus {
		if s.getStatus != nil {
			return interfaces.AgentStatus(s.getStatus())
		}
		return interfaces.StatusIdle
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/state", s.handleState)

	staticDir := filepath.Join(".", "internal", "server", "static")
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	var handler http.Handler = mux
	handler = loggingMiddleware(handler)
	if s.cfg.Token != "" {
		handler = (&TokenAuth{Token: s.cfg.Token}).Middleware(handler)
	}

	addr := fmt.Sprintf(":%d", s.cfg.Port)
	log.Printf("[SERVER] Companion UI at http://localhost%s", addr)
	go func() {
		if err := http.ListenAndServe(addr, handler); err != nil {
			log.Printf("[SERVER] HTTP error: %v", err)
		}
	}()

	return nil
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade: %v", err)
		return
	}

	client := &Client{
		hub:  s.hub,
		conn: conn,
		send: make(chan []byte, 256),
		id:   r.RemoteAddr,
	}

	s.hub.register <- client
	go client.writePump()
	go client.readPump()

	// Send initial state on connect
	status := "idle"
	if s.getStatus != nil {
		status = s.getStatus()
	}
	raw := fmt.Sprintf(`{"method":"status.changed","params":{"status":"%s"}}`, status)
	client.send <- []byte(raw)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := "idle"
	if s.getStatus != nil {
		status = s.getStatus()
	}
	fmt.Fprintf(w, `{"status":"ok","agent":"%s","speaking":%v,"tts_playing":%v,"clients":%d}`,
		status, atomic.LoadInt32(s.isSpeaking) != 0, atomic.LoadInt32(s.ttsPlaying) != 0, s.hub.ClientCount())
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := "idle"
	if s.getStatus != nil {
		status = s.getStatus()
	}
	fmt.Fprintf(w, `{"status":"%s"}`, status)
}
