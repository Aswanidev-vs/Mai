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
	MethodChatInput       = "chat.input"
	MethodConfigUpdate    = "config.update"
	MethodStateRequest    = "state.request"
	MethodAudioInput      = "audio.input"
	MethodAudioInputStart = "audio.input.start"
	MethodAudioInputStop  = "audio.input.stop"
)

// Server → Client notifications
const (
	NotifChatResponse  = "chat.response"
	NotifStatusChanged = "status.changed"
	NotifTTSChunk      = "tts.chunk"
	NotifEmotionDetect = "emotion.detected"
	NotifEmotionMai    = "emotion.mai"
	NotifConfigChanged = "config.changed"
	NotifMemoryUpdate  = "memory.update"
	NotifDance         = "companion.dance"
	NotifAction        = "companion.action"
	// NotifPartialTranscript carries an in-progress ASR result (final=false) and
	// NotifFinalTranscript the authoritative one for the utterance (final=true).
	NotifPartialTranscript = "asr.partial"
	NotifFinalTranscript   = "asr.final"
)

// ChatInputParams is the payload for chat.input.
type ChatInputParams struct {
	Text string `json:"text"`
}

// ActionParams carries an explicit action/motion requested by the user.
type ActionParams struct {
	Action   string  `json:"action"`
	Duration float64 `json:"duration,omitempty"`
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
	Audio      string `json:"audio"`
	SampleRate int    `json:"sample_rate"`
	Done       bool   `json:"done"`
	// Muted marks audio the companion must play silently: the speaker output
	// comes from the local device, and the tab only needs the same timeline
	// to drive lip sync and the speaking state.
	Muted bool `json:"muted,omitempty"`
	// DurationSeconds is the complete sentence duration, repeated on each PCM
	// chunk so the browser can normalize visemes before streaming finishes.
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
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

// PartialTranscriptParams carries one live ASR result for the utterance the user
// is currently speaking. Interim results (Final=false) are provisional: the
// recognizer may revise or replace the whole string, so a client should render
// them as a single replaceable caption rather than as appended tokens. The
// authoritative result arrives as NotifFinalTranscript with Final=true.
type PartialTranscriptParams struct {
	Text  string `json:"text"`
	Final bool   `json:"final"`
	// TurnID ties every interim and final result to the utterance it belongs to,
	// so a client can discard partials left over from a turn it already finished.
	// The server may leave this at its zero value until the turn coordinator
	// lands; until then clients must treat a missing turn_id as "current
	// utterance" rather than assuming the ID never changes.
	TurnID string `json:"turn_id,omitempty"`
}
