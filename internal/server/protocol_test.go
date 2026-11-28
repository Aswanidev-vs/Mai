package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPartialTranscriptParamsJSONShape(t *testing.T) {
	tests := []struct {
		name   string
		params PartialTranscriptParams
		want   string
	}{
		{
			name:   "interim omits unset turn id",
			params: PartialTranscriptParams{Text: "hello wor"},
			want:   `{"text":"hello wor","final":false}`,
		},
		{
			name:   "final carries turn id",
			params: PartialTranscriptParams{Text: "hello world", Final: true, TurnID: "turn-7"},
			want:   `{"text":"hello world","final":true,"turn_id":"turn-7"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.params)
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(data))
		})
	}
}

func TestPresenceParamsJSONShape(t *testing.T) {
	data, err := json.Marshal(PresenceParams{Phase: "thinking", Emotion: "calm", Intensity: 0.4, Target: "chrome", TurnID: "t3"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"phase":"thinking","emotion":"calm","intensity":0.4,"target":"chrome","turn_id":"t3"}`, string(data))
}

func TestProsodyParamsJSONShape(t *testing.T) {
	data, err := json.Marshal(ProsodyParams{PauseRatio: 0.4, PitchVariance: 0.7, RMS: 0.2, TurnID: "t1"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"pause_ratio":0.4,"pitch_variance":0.7,"rms":0.2,"turn_id":"t1"}`, string(data))
}

func TestPhaseParamsCarriesPhaseAndTurn(t *testing.T) {
	data, err := json.Marshal(PhaseParams{Phase: "thinking", TurnID: "t42"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"phase":"thinking","turn_id":"t42"}`, string(data))
}

func TestPartialTranscriptFinalDistinguishableFromInterim(t *testing.T) {
	interim, err := json.Marshal(PartialTranscriptParams{Text: "mai are you"})
	require.NoError(t, err)
	final, err := json.Marshal(PartialTranscriptParams{Text: "mai are you there", Final: true})
	require.NoError(t, err)
	assert.NotEqual(t, string(interim), string(final))

	var decoded PartialTranscriptParams
	require.NoError(t, json.Unmarshal(final, &decoded))
	assert.True(t, decoded.Final)
	assert.Equal(t, "mai are you there", decoded.Text)
	assert.Empty(t, decoded.TurnID)
}

// The wire path must pick asr.partial vs asr.final from Final and drop empty
// results; NewHub is used without Run so the broadcast channel is read directly.
func TestBridgePublishPartialTranscriptSelectsMethod(t *testing.T) {
	tests := []struct {
		name       string
		params     PartialTranscriptParams
		wantMethod string
		wantEmit   bool
	}{
		{"interim", PartialTranscriptParams{Text: "hi"}, NotifPartialTranscript, true},
		{"final", PartialTranscriptParams{Text: "hi there", Final: true}, NotifFinalTranscript, true},
		{"empty interim dropped", PartialTranscriptParams{}, "", false},
		{"empty final dropped", PartialTranscriptParams{Final: true}, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewHub()
			b := &Bridge{hub: hub}
			b.PublishPartialTranscript(tc.params)

			var raw []byte
			select {
			case out := <-hub.broadcast:
				raw = out.data
			case <-time.After(200 * time.Millisecond):
				raw = nil
			}

			if !tc.wantEmit {
				assert.Nil(t, raw)
				return
			}

			require.NotNil(t, raw)
			var msg WSMessage
			require.NoError(t, json.Unmarshal(raw, &msg))
			assert.Equal(t, tc.wantMethod, msg.Method)

			var params PartialTranscriptParams
			require.NoError(t, json.Unmarshal(msg.Params, &params))
			assert.Equal(t, tc.params.Text, params.Text)
			assert.Equal(t, tc.params.Final, params.Final)
		})
	}
}

func TestAutomationPhaseParamsJSONShape(t *testing.T) {
	params := AutomationPhaseParams{
		Phase:      "walk",
		Target:     "app",
		App:        "whatsapp",
		Contact:    "Priya",
		Reason:     "opening chat",
		DurationMS: 1800,
	}
	data, err := json.Marshal(params)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"phase": "walk",
		"target": "app",
		"app": "whatsapp",
		"contact": "Priya",
		"reason": "opening chat",
		"duration_ms": 1800
	}`, string(data))

	var decoded AutomationPhaseParams
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, params, decoded)
	assert.Equal(t, "companion.automation", NotifAutomation)
}
