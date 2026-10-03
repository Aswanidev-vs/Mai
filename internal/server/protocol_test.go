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
			case raw = <-hub.broadcast:
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
