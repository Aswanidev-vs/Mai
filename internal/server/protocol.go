package server

import "encoding/json"

// WSMessage represents a JSON-RPC style message over WebSocket.
type WSMessage struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *WSError        `json:"error,omitempty"`
}

// WSError represents an error in a WSMessage.
type WSError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Client → Server methods
const (
	MethodChatInput        = "chat.input"
	MethodConfigUpdate     = "config.update"
	MethodStateRequest     = "state.request"
	MethodAudioInput       = "audio.input"
	MethodAudioInputStart  = "audio.input.start"
	MethodAudioInputStop   = "audio.input.stop"
)

// Server → Client notifications
const (
	NotifChatResponse  = "chat.response"
	NotifStatusChanged = "status.changed"
	NotifTTSChunk      = "tts.chunk"
	NotifEmotionDetect = "emotion.detected"
	NotifConfigChanged = "config.changed"
	NotifMemoryUpdate  = "memory.update"
	NotifDance         = "companion.dance"
)

// ChatInputParams is the payload for chat.input.
type ChatInputParams struct {
	Text string `json:"text"`
}

// ChatResponseChunk is streamed back for chat.response.
type ChatResponseChunk struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// StatusChangedParams is sent when agent status changes.
type StatusChangedParams struct {
	Status string `json:"status"`
}

// TTSChunkParams carries base64-encoded audio.
type TTSChunkParams struct {
	Audio     string `json:"audio"`
	SampleRate int   `json:"sample_rate"`
	Done      bool  `json:"done"`
}

// EmotionDetectedParams carries the detected emotion state.
type EmotionDetectedParams struct {
	Emotion   string  `json:"emotion"`
	Intensity float64 `json:"intensity"`
}

// ConfigUpdateParams is sent by the client to change settings.
type ConfigUpdateParams struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

// ConfigChangedParams is broadcast when config changes.
type ConfigChangedParams struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

// AudioInputParams carries raw PCM (base64 int16) from the browser microphone.
type AudioInputParams struct {
	Audio      string `json:"audio"`
	SampleRate int    `json:"sample_rate"`
}
