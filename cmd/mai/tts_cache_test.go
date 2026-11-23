package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSynth records calls and emits one deterministic chunk so a cache hit can
// be told apart from a real synthesis by the call count alone.
func fakeSynth(calls *int, chunk []float32) func(string, float32, func([]float32) bool) {
	return func(_ string, _ float32, cb func([]float32) bool) {
		*calls++
		// Deliberately hand out the same backing slice every time: a cache that
		// aliases caller memory instead of copying would be caught below.
		cb(chunk)
	}
}

func TestFixedTTSCacheReplaysOnlyFixedUtterances(t *testing.T) {
	clearFixedTTSCache()
	t.Cleanup(clearFixedTTSCache)

	chunk := []float32{0.1, -0.2, 0.3}
	calls := 0
	synth := fakeSynth(&calls, chunk)
	var delivered [][]float32
	deliver := func(s []float32) bool {
		delivered = append(delivered, append([]float32(nil), s...))
		return true
	}

	synthesizeFixed("System ready.", 1, 1, 1, 24000, synth, deliver)
	require.Equal(t, 1, calls)
	require.Len(t, delivered, 1)

	// Same key: replay without touching the engine.
	synthesizeFixed("System ready.", 1, 1, 1, 24000, synth, deliver)
	assert.Equal(t, 1, calls, "a fixed prompt must be synthesized only once")
	require.Len(t, delivered, 2)
	assert.Equal(t, delivered[0], delivered[1])

	// The replayed buffer must not alias the cached one.
	delivered[1][0] = 99
	synthesizeFixed("System ready.", 1, 1, 1, 24000, synth, deliver)
	assert.Equal(t, 1, calls)
	assert.Equal(t, float32(0.1), delivered[2][0], "mutating a replay corrupted the cached PCM")

	// A user-dependent reply is never cached, even if the text repeats.
	synthesizeFixed("What is the weather?", 1, 1, 1, 24000, synth, deliver)
	synthesizeFixed("What is the weather?", 1, 1, 1, 24000, synth, deliver)
	assert.Equal(t, 3, calls, "dynamic responses must always be synthesized")
}

func TestFixedTTSCacheKeyIncludesVoiceParams(t *testing.T) {
	clearFixedTTSCache()
	t.Cleanup(clearFixedTTSCache)

	calls := 0
	synth := fakeSynth(&calls, []float32{0.5})
	deliver := func([]float32) bool { return true }
	greeting := greetingPools[0][0]

	synthesizeFixed(greeting, 1, 1, 1, 24000, synth, deliver)
	for _, p := range []struct {
		volume, pitch float32
		rate          int
	}{
		{0.9, 1, 24000},
		{1, 0.95, 24000},
		{1, 1, 44100},
	} {
		synthesizeFixed(greeting, 1, p.volume, p.pitch, p.rate, synth, deliver)
	}
	assert.Equal(t, 4, calls, "every voice parameter in the key must get its own entry")
	synthesizeFixed(greeting, 1, 1, 1, 24000, synth, deliver)
	assert.Equal(t, 4, calls, "the original key must still hit")
}

func TestFixedTTSCacheDoesNotCacheStoppedGeneration(t *testing.T) {
	clearFixedTTSCache()
	t.Cleanup(clearFixedTTSCache)

	calls := 0
	synth := func(text string, speed float32, cb func([]float32) bool) {
		calls++
		cb([]float32{0.25})
	}
	synthesizeFixed("Sorry, I didn't catch that. Could you say it again?", 1, 1, 1, 24000, synth,
		func([]float32) bool { return false })
	synthesizeFixed("Sorry, I didn't catch that. Could you say it again?", 1, 1, 1, 24000, synth,
		func([]float32) bool { return true })
	assert.Equal(t, 2, calls, "a barge-in-stopped generation must not be cached")
}

func TestFixedTTSCacheIsBounded(t *testing.T) {
	clearFixedTTSCache()
	t.Cleanup(clearFixedTTSCache)

	deliver := func([]float32) bool { return true }
	// More distinct keys than the cache can hold. Vary speed on one real fixed
	// prompt; text this is not would bypass the cache and test nothing.
	for i := 0; i < fixedTTSCacheMax+10; i++ {
		synthesizeFixed(greetingPools[0][0], float32(i+1), 1, 1, 24000,
			func(_ string, _ float32, cb func([]float32) bool) { cb([]float32{0.5}) }, deliver)
	}
	assert.LessOrEqual(t, fixedTTSCacheSize.Load(), int64(fixedTTSCacheMax))
}

func TestGreetingAllowlistCoversEveryPoolEntry(t *testing.T) {
	for _, pool := range greetingPools {
		for _, greeting := range pool {
			assert.True(t, isFixedUtterance(greeting), "greeting %q is not cacheable", greeting)
		}
	}
	assert.False(t, isFixedUtterance("Tell me about the weather."))
}
