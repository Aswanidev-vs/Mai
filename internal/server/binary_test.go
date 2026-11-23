package server

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/mai/pkg/interfaces"
)

type captureBus struct {
	events []interfaces.Event
	subs   map[string][]interfaces.EventHandler
}

func (b *captureBus) Publish(e interfaces.Event) error {
	b.events = append(b.events, e)
	for _, h := range b.subs[e.Type] {
		h(e)
	}
	return nil
}
func (b *captureBus) Subscribe(t string, h interfaces.EventHandler) interfaces.Subscription {
	if b.subs == nil {
		b.subs = make(map[string][]interfaces.EventHandler)
	}
	b.subs[t] = append(b.subs[t], h)
	return nil
}
func (b *captureBus) SubscribeAsync(t string, h interfaces.EventHandler) interfaces.Subscription {
	return b.Subscribe(t, h)
}
func (b *captureBus) RequestResponse(interfaces.Event, time.Duration) (interfaces.Event, error) {
	return interfaces.Event{}, nil
}

func audioFrame(rate uint32, pcm []byte) []byte {
	frame := make([]byte, audioInputHeaderSize+len(pcm))
	frame[0] = frameTagAudioInput
	binary.LittleEndian.PutUint32(frame[1:5], rate)
	copy(frame[audioInputHeaderSize:], pcm)
	return frame
}

func TestBinaryAudioFramePublishesNormalizedSamples(t *testing.T) {
	bus := &captureBus{}
	hub := NewHub()
	hub.eventBus = bus
	pcm := []byte{0x00, 0x40, 0x00, 0x80} // 0.5, -1.0

	hub.HandleBinaryMessage(&Client{id: "browser"}, audioFrame(16000, pcm))

	require.Len(t, bus.events, 1)
	e := bus.events[0]
	assert.Equal(t, "perception.audio.frame", e.Type)
	assert.Equal(t, "ws-server", e.Source)
	assert.Equal(t, 16000, e.Payload["sample_rate"])
	assert.Equal(t, "browser", e.Payload["client"])
	assert.Equal(t, []float32{0.5, -1.0}, e.Payload["samples"])
}

func TestBinaryAudioFrameRejectsMalformedInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"wrong tag", append([]byte{0x54}, audioFrame(16000, []byte{0, 0})[1:]...)},
		{"odd sample count", audioFrame(16000, []byte{0})},
		{"no samples", audioFrame(16000, nil)},
		{"zero sample rate", audioFrame(0, []byte{0, 0})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := &captureBus{}
			hub := NewHub()
			hub.eventBus = bus
			hub.HandleBinaryMessage(&Client{id: "browser"}, tc.frame)
			assert.Empty(t, bus.events)
		})
	}
}

func TestTTSChunkFrameRoundTrip(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0xff, 0x7f}
	frame := encodeTTSChunkFrame(TTSChunkParams{
		PCM: pcm, SampleRate: 24000, Done: true, Muted: true, DurationSeconds: 2.25,
	})

	require.Equal(t, byte(frameTagTTSChunk), frame[0])
	assert.Equal(t, uint32(24000), binary.LittleEndian.Uint32(frame[1:5]))
	assert.Equal(t, byte(ttsFlagDone|ttsFlagMuted), frame[5])
	assert.Equal(t, 2.25, math.Float64frombits(binary.LittleEndian.Uint64(frame[6:14])))
	assert.Equal(t, pcm, frame[ttsChunkHeaderSize:])
}

func TestBridgeBroadcastsTTSChunkAsBinaryFrame(t *testing.T) {
	hub := NewHub()
	bus := &captureBus{}
	NewBridge(bus, hub, nil)

	pcm := []byte{0x00, 0x40, 0x00, 0x80}
	bus.Publish(interfaces.Event{
		Type: "tts.audio.chunk",
		Payload: map[string]interface{}{
			"audio":            base64.StdEncoding.EncodeToString(pcm),
			"sample_rate":      16000,
			"done":             true,
			"muted":            true,
			"duration_seconds": 1.5,
		},
	})

	var frame []byte
	select {
	case out := <-hub.broadcast:
		assert.True(t, out.binary, "TTS chunks must go out as binary frames")
		frame = out.data
	case <-time.After(time.Second):
		t.Fatal("no binary TTS frame broadcast")
	}
	require.GreaterOrEqual(t, len(frame), ttsChunkHeaderSize)
	assert.Equal(t, byte(frameTagTTSChunk), frame[0])
	assert.Equal(t, uint32(16000), binary.LittleEndian.Uint32(frame[1:5]))
	assert.Equal(t, byte(ttsFlagDone|ttsFlagMuted), frame[5])
	assert.Equal(t, 1.5, math.Float64frombits(binary.LittleEndian.Uint64(frame[6:14])))
	assert.Equal(t, pcm, frame[ttsChunkHeaderSize:])
}
