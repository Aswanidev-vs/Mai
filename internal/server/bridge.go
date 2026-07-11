package server

import (
	"context"
	"log"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

// Bridge connects the Mai event bus to WebSocket clients.
type Bridge struct {
	hub          *Hub
	getStatus    func() interfaces.AgentStatus
	cancel       context.CancelFunc
}

// NewBridge creates a Bridge, subscribes to bus events, and starts status polling.
func NewBridge(bus interfaces.EventBus, hub *Hub, getStatus func() interfaces.AgentStatus) *Bridge {
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bridge{hub: hub, getStatus: getStatus, cancel: cancel}
	b.subscribe(bus)
	b.startStatusPoller(ctx)
	return b
}

func (b *Bridge) subscribe(bus interfaces.EventBus) {
	// WS chat input → orchestrator: re-publish as perception.audio.transcription
	bus.Subscribe("ws.chat.input", func(event interfaces.Event) {
		text, _ := event.Payload["text"].(string)
		if text == "" {
			return
		}
		log.Printf("[BRIDGE] WS input → orchestrator: %s", text)
		bus.Publish(interfaces.Event{
			Type:   "perception.audio.transcription",
			Source: "ws-bridge",
			Payload: map[string]interface{}{
				"text": text,
			},
		})
	})

	// Orchestrator response → WS clients
	bus.Subscribe("action.tts.request", func(event interfaces.Event) {
		text, _ := event.Payload["text"].(string)
		if text == "" {
			return
		}
		b.hub.BroadcastNotification(NotifChatResponse, ChatResponseChunk{Text: text, Done: true})
	})

	// TTS audio chunks → WS clients (for browser-side lip sync)
	bus.Subscribe("tts.audio.chunk", func(event interfaces.Event) {
		audio, _ := event.Payload["audio"].(string)
		sampleRate, _ := event.Payload["sample_rate"].(int)
		done, _ := event.Payload["done"].(bool)
		// Skip empty non-done chunks, but always forward done signals
		if audio == "" && !done {
			return
		}
		if sampleRate == 0 {
			sampleRate = 24000
		}
		b.hub.BroadcastNotification(NotifTTSChunk, TTSChunkParams{
			Audio:      audio,
			SampleRate: sampleRate,
			Done:       done,
		})
	})

	log.Println("[BRIDGE] Event bus bridge active")
}

// startStatusPoller periodically checks agent status and broadcasts changes.
func (b *Bridge) startStatusPoller(ctx context.Context) {
	go func() {
		var lastStatus string
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if b.getStatus == nil {
					continue
				}
				status := string(b.getStatus())
				if status != lastStatus {
					lastStatus = status
					b.hub.BroadcastNotification(NotifStatusChanged, StatusChangedParams{Status: status})
				}
			}
		}
	}()
}

func (b *Bridge) Stop() {
	b.cancel()
}
