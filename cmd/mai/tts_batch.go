package main

import (
	"strings"
	"sync/atomic"
)

// ttsItem is one sentence flowing from the orchestrator's streaming handoff to
// the TTS renderer.
type ttsItem struct {
	text    string
	speed   float32
	volume  float32   // emotion-adaptive loudness; applied as a sample gain in renderSentence
	pitch   float32   // emotion/style pitch ratio (1 = unchanged); honoured when tts.pitch_shift
	seq     int64     // orchestrator turn id; every sentence of one reply shares it
	final   bool      // end-of-turn marker: nothing follows for this turn
	samples []float32 // pre-rendered audio (filled by the renderer, native rate)
}

// drainTTSBatch merges the sentences already queued behind first into one
// synthesis request, for a TTS engine that pays a large FIXED cost per call.
//
// Pocket is the motivating case: every Generate() builds a fresh LM state and
// runs the EOS search, and only reuses state within a call. Measured realtime
// factor on one machine: 6 chars 1.13x, 24 chars 2.04x, 61 chars 2.23x,
// 100 chars 2.20x — so the short sentences the LLM streaming handoff emits
// synthesized barely faster than they played, and silent gaps appeared between
// sentences. Merging what is already queued amortizes that cost.
//
// A non-absorbable item (an end-of-turn marker, or a sentence from a newer
// turn) is returned rather than re-queued, because re-queuing would reorder
// the queue; the caller must process it before reading the channel again.
//
// The drain is non-blocking, so it only ever uses what is already waiting and
// never adds latency to the first sentence.
func drainTTSBatch(ch <-chan ttsItem, first ttsItem, maxChars int, latestTurn *atomic.Int64) (text string, deferred *ttsItem, open bool) {
	text = first.text
	total := len(text)
	for total < maxChars {
		select {
		case next, ok := <-ch:
			if !ok {
				return text, nil, false
			}
			// Never absorb a marker or another turn: the player's end-of-turn
			// logic is order-sensitive, and a batch spanning turns would speak
			// a superseded answer over a fresh one.
			if next.final || next.seq != first.seq {
				return text, &next, true
			}
			if next.seq > 0 && latestTurn != nil && next.seq < latestTurn.Load() {
				continue // superseded; drop now instead of after synthesis
			}
			part := strings.TrimSpace(next.text)
			if part == "" {
				continue
			}
			text += " " + part
			total += len(part)
		default:
			return text, nil, true
		}
	}
	return text, nil, true
}
