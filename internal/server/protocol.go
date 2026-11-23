package server

import (
	"encoding/binary"
	"encoding/json"
	"math"
)

// WebSocket text frames are JSON-RPC messages (WSMessage). Binary frames carry
// raw PCM instead of base64; their first byte is a tag:
//
//	audio input (browser → server), tag 0x41:
//	  byte 0:    tag
//	  bytes 1-4: sample rate, uint32 little-endian
//	  bytes 5+:  int16 little-endian mono PCM
//
//	TTS chunk (server → browser), tag 0x54:
//	  byte 0:     tag
//	  bytes 1-4:  sample rate, uint32 little-endian
//	  byte 5:     flags: bit0 = done, bit1 = muted
//	  bytes 6-13: duration seconds, float64 little-endian (lip-sync timeline)
//	  bytes 14+:  int16 little-endian PCM
const (
	frameTagAudioInput = 0x41
	frameTagTTSChunk   = 0x54

	audioInputHeaderSize = 5
	ttsChunkHeaderSize   = 14

	ttsFlagDone  = 0x01
	ttsFlagMuted = 0x02
)

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
	NotifAutomation    = "companion.automation"
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

// AutomationPhaseParams drives the companion overlay through a desktop
// automation workflow. It deliberately carries no message body: the overlay
// only needs the phase and the semantic target.
type AutomationPhaseParams struct {
	Phase      string `json:"phase"`
	Target     string `json:"target,omitempty"`
	App        string `json:"app,omitempty"`
	Contact    string `json:"contact,omitempty"`
	Reason     string `json:"reason,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
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

// TTSChunkParams is encoded as a binary frame, not a JSON notification.
type TTSChunkParams struct {
	// PCM is int16 little-endian audio; it has no JSON representation.
	PCM        []byte `json:"-"`
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

// encodeTTSChunkFrame packs a TTS chunk into the binary frame documented above.
func encodeTTSChunkFrame(p TTSChunkParams) []byte {
	frame := make([]byte, ttsChunkHeaderSize+len(p.PCM))
	frame[0] = frameTagTTSChunk
	binary.LittleEndian.PutUint32(frame[1:5], uint32(p.SampleRate))
	if p.Done {
		frame[5] |= ttsFlagDone
	}
	if p.Muted {
		frame[5] |= ttsFlagMuted
	}
	binary.LittleEndian.PutUint64(frame[6:14], math.Float64bits(p.DurationSeconds))
	copy(frame[ttsChunkHeaderSize:], p.PCM)
	return frame
}

// parseAudioInputFrame validates a microphone frame and returns its payload.
func parseAudioInputFrame(raw []byte) (sampleRate int, pcm []byte, ok bool) {
	if len(raw) <= audioInputHeaderSize || (len(raw)-audioInputHeaderSize)%2 != 0 {
		return 0, nil, false
	}
	if raw[0] != frameTagAudioInput {
		return 0, nil, false
	}
	rate := binary.LittleEndian.Uint32(raw[1:5])
	if rate == 0 || rate > 384000 {
		return 0, nil, false
	}
	return int(rate), raw[audioInputHeaderSize:], true
}
