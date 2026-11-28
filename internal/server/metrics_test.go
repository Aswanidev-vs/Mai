package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/mai/internal/observability"
)

func TestHandleMetricsExposesStagePercentiles(t *testing.T) {
	observability.ResetForTest()
	observability.RecordStage("asr", 20*time.Millisecond)

	rec := httptest.NewRecorder()
	(&Server{}).handleMetrics(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.InDelta(t, 20.0, body["latency_ms{operation=asr}_p95"], 0.001)
	assert.Contains(t, body, "latency_ms{operation=asr}_p50")
}

func TestHubKeepsPresenceSnapshot(t *testing.T) {
	hub := NewHub()
	assert.Empty(t, hub.Presence().Phase)
	hub.SetPresence(PresenceParams{Phase: "speaking", Emotion: "happy", Intensity: 0.7, TurnID: "t9"})

	got := hub.Presence()
	assert.Equal(t, "speaking", got.Phase)
	assert.Equal(t, "happy", got.Emotion)
	assert.Equal(t, "t9", got.TurnID)
}

func TestBroadcastNotificationCarriesTurnID(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	client := &Client{hub: hub, conn: nil, send: make(chan outboundMessage, 1), id: "test"}
	hub.register <- client

	hub.BroadcastNotification(NotifChatResponse, ChatResponseChunk{Text: "hi"}, "t42")
	msg := <-client.send

	var decoded WSMessage
	require.NoError(t, json.Unmarshal(msg.data, &decoded))
	assert.Equal(t, "t42", decoded.TurnID)
	assert.Equal(t, NotifChatResponse, decoded.Method)
}
