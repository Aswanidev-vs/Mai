package main

import (
	"sync"
	"sync/atomic"
	"time"
)

// greetingPools holds the fixed openers pickGreeting rotates through. It lives
// beside the allowlist so adding a greeting cannot silently turn it into an
// uncached (and unnecessarily re-synthesized) request.
var greetingPools = [][]string{
	{
		"Good morning. What are we getting into today?",
		"Morning. I'm awake. What do you need?",
		"Good morning, Aswani-kun.",
	},
	{
		"Hey. What's on your mind?",
		"I'm here. What do we have today?",
		"Good afternoon. What do you need?",
	},
	{
		"Evening. I'm with you.",
		"Hey, good evening. What are we doing?",
		"Evening, Aswani-kun. I'm here.",
	},
	{
		"Still up? I'm here. What do you need?",
		"Night owl hours. What's on your mind?",
		"Late one, hm? Say the word.",
	},
}

func greetingForHour(hour int) []string {
	switch {
	case hour >= 5 && hour < 12:
		return greetingPools[0]
	case hour >= 12 && hour < 17:
		return greetingPools[1]
	case hour >= 17 && hour < 22:
		return greetingPools[2]
	default:
		return greetingPools[3]
	}
}

// pickGreeting returns a persona-consistent, time-of-day aware opener instead
// of the old JARVIS-style "Yes Sir / At your service" lines. Rotated per
// hour so repeated wake-ups don't sound scripted ("greet by context" — the
// single biggest companionship cue per companion-AI research).
func pickGreeting() string {
	pool := greetingForHour(time.Now().Hour())
	return pool[time.Now().UnixNano()%int64(len(pool))]
}

// isFixedUtterance reports whether text is a prompt Mai chose herself. Only
// these may be cached: a user-dependent reply must never be replayed from an
// earlier question.
func isFixedUtterance(text string) bool {
	switch text {
	case "System ready.", "Sorry, I didn't catch that. Could you say it again?":
		return true
	}
	for _, pool := range greetingPools {
		for _, greeting := range pool {
			if text == greeting {
				return true
			}
		}
	}
	return false
}

type ttsCacheKey struct {
	text       string
	speed      float32
	volume     float32
	pitch      float32
	sampleRate int
}

// fixedTTSCache stores raw engine PCM for fixed prompts. Pocket pays a large
// fixed cost per Generate call (fresh LM state + EOS search), which dominates
// these short lines; replaying the same PCM removes that cost entirely.
var fixedTTSCache sync.Map // map[ttsCacheKey][]float32

const fixedTTSCacheMax = 64

var fixedTTSCacheSize atomic.Int64

func cacheFixedPCM(key ttsCacheKey, pcm []float32) {
	if fixedTTSCacheSize.Load() >= fixedTTSCacheMax {
		return
	}
	if _, loaded := fixedTTSCache.LoadOrStore(key, pcm); !loaded {
		fixedTTSCacheSize.Add(1)
	}
}

// clearFixedTTSCache exists for the cache tests, which each need a clean map.
func clearFixedTTSCache() {
	fixedTTSCache.Range(func(key, _ any) bool {
		fixedTTSCache.Delete(key)
		return true
	})
	fixedTTSCacheSize.Store(0)
}

// synthesizeFixed replays cached PCM for a fixed prompt, or synthesizes it via
// synth and caches the result. Every other text goes straight to synth so a
// user-dependent answer is never cached. synth and deliver both stream chunks
// to a callback whose false return stops generation (barge-in); a stopped
// generation is deliberately not cached.
func synthesizeFixed(text string, speed, volume, pitch float32, sampleRate int,
	synth func(string, float32, func([]float32) bool), deliver func([]float32) bool) {
	key := ttsCacheKey{text: text, speed: speed, volume: volume, pitch: pitch, sampleRate: sampleRate}
	if !isFixedUtterance(text) {
		synth(text, speed, deliver)
		return
	}
	if cached, ok := fixedTTSCache.Load(key); ok {
		// The cached buffer is shared; hand the caller its own copy so a
		// downstream resampler cannot mutate what future plays will replay.
		deliver(append([]float32(nil), cached.([]float32)...))
		return
	}
	var pcm []float32
	stopped := false
	synth(text, speed, func(samples []float32) bool {
		if len(samples) > 0 {
			pcm = append(pcm, samples...)
		}
		keep := deliver(samples)
		if !keep {
			stopped = true
		}
		return keep
	})
	if !stopped && len(pcm) > 0 {
		cacheFixedPCM(key, pcm)
	}
}
