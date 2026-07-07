package server

import (
	"encoding/json"
	"log"

	"github.com/user/mai/pkg/interfaces"
)

// HandleMessage processes an incoming WS message and routes it.
func (h *Hub) HandleMessage(client *Client, raw []byte) {
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		h.sendError(client, "", -32700, "Parse error")
		return
	}

	switch msg.Method {
	case MethodChatInput:
		h.handleChatInput(client, msg)
	case MethodStateRequest:
		h.handleStateRequest(client, msg)
	case MethodConfigUpdate:
		h.handleConfigUpdate(client, msg)
	default:
		if msg.Method != "" {
			h.sendError(client, msg.ID, -32601, "Method not found: "+msg.Method)
		}
	}
}

func (h *Hub) handleChatInput(client *Client, msg WSMessage) {
	var params ChatInputParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		h.sendError(client, msg.ID, -32602, "Invalid params")
		return
	}
	if params.Text == "" {
		h.sendError(client, msg.ID, -32602, "Empty text")
		return
	}

	log.Printf("[WS] Chat input from %s: %s", client.id, params.Text)

	// Publish to event bus — the orchestrator will pick it up
	event := interfaces.Event{
		Type:   "ws.chat.input",
		Source: "ws-server",
		Payload: map[string]interface{}{
			"text":    params.Text,
			"client":  client.id,
		},
	}
	if h.eventBus != nil {
		h.eventBus.Publish(event)
	}

	// Send ack
	if msg.ID != "" {
		h.sendResult(client, msg.ID, json.RawMessage(`{"ok":true}`))
	}
}

func (h *Hub) handleStateRequest(client *Client, msg WSMessage) {
	status := "idle"
	if h.getStatusFunc != nil {
		status = h.getStatusFunc()
	}
	result, _ := json.Marshal(StatusChangedParams{Status: status})
	h.sendResult(client, msg.ID, result)
}

func (h *Hub) handleConfigUpdate(client *Client, msg WSMessage) {
	var params ConfigUpdateParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		h.sendError(client, msg.ID, -32602, "Invalid params")
		return
	}

	log.Printf("[WS] Config update from %s: %s = %v", client.id, params.Key, params.Value)

	// Publish config change event
	event := interfaces.Event{
		Type:   "ws.config.update",
		Source: "ws-server",
		Payload: map[string]interface{}{
			"key":   params.Key,
			"value": params.Value,
		},
	}
	if h.eventBus != nil {
		h.eventBus.Publish(event)
	}

	if msg.ID != "" {
		h.sendResult(client, msg.ID, json.RawMessage(`{"ok":true}`))
	}
}

func (h *Hub) sendResult(client *Client, id string, result json.RawMessage) {
	msg := WSMessage{ID: id, Result: result}
	data, _ := json.Marshal(msg)
	h.SendToClient(client, data)
}

func (h *Hub) sendError(client *Client, id string, code int, message string) {
	msg := WSMessage{
		ID:    id,
		Error: &WSError{Code: code, Message: message},
	}
	data, _ := json.Marshal(msg)
	h.SendToClient(client, data)
}

// BroadcastNotification sends a server notification to all clients.
func (h *Hub) BroadcastNotification(method string, params interface{}) {
	data, _ := json.Marshal(params)
	msg := WSMessage{Method: method, Params: data}
	raw, _ := json.Marshal(msg)
	h.BroadcastToAll(raw)
}
