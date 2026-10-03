package session

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Kind names what opened a turn. The distinction matters to the TTS path: a
// wake word is a deliberate summons and may answer immediately, while user
// speech is mid-conversation and should not be answered over the top of the
// previous answer.
type Kind string

const (
	KindWakeWord   Kind = "wake_word"
	KindUserSpeech Kind = "user_speech"
	KindPushToTalk Kind = "push_to_talk"
	KindBrowserMic Kind = "browser_mic"
)

// Turn is one conversational turn: the unit that the LLM stream, the TTS
// synthesiser and the playback device all hang their work on, and the unit a
// cancellation tears down.
//
// Every field a hot-path reader touches is either immutable after minting or
// atomic, so the audio callback can inspect a *Turn without taking a lock.
type Turn struct {
	// ID is the monotonic turn number. Turn IDs never repeat and never go
	// backwards, so "is this the turn I think it is" is answerable from a
	// number alone — the alternative in the tree today is comparing scattered
	// counters that were each minted by a different subsystem.
	ID uint64

	// GenerationID is the assistant-audio generation this turn was minted in.
	// It is a snapshot for attribution, not for staleness: a barge-in inside
	// this same turn advances the manager's generation past it, and
	// Manager.IsStale is what decides staleness. See Manager.Generation.
	GenerationID uint64

	// Kind records what opened the turn.
	Kind Kind

	// Ctx is cancelled when the turn is superseded, interrupted or ends.
	// In-flight synthesis and LLM streaming hang their selects on it, which is
	// the whole reason the turn exists: before this, cancellation was a bare
	// atomic flag with nowhere to put a context.
	Ctx context.Context

	// StartedAt is when the turn was minted, for end-to-end turn latency.
	StartedAt time.Time

	cancel       context.CancelFunc
	cancelOnce   sync.Once
	cancelReason atomic.Value // string, written once
	state        atomic.Value // State
}

func newTurn(id, generation uint64, kind Kind, ctx context.Context, cancel context.CancelFunc) *Turn {
	t := &Turn{
		ID:           id,
		GenerationID: generation,
		Kind:         kind,
		Ctx:          ctx,
		StartedAt:    time.Now(),
		cancel:       cancel,
	}
	// A turn is born listening: whatever Mai does inside it — answer, get
	// interrupted, get interrupted and answer again — happens under one ID.
	t.state.Store(StateListening)
	return t
}

// Cancel ends the turn and records why. It is idempotent and safe to call
// concurrently from any number of goroutines: the first reason wins, because
// that is the one that actually killed the turn, and later calls are no-ops
// rather than errors. Both the barge-in path and the audio callback may reach
// for the same turn at the same moment and neither should win a race.
//
// The cancel func itself is deliberately not exposed: a caller holding the raw
// func could cancel the context without recording a reason, which would leave
// the one field that explains an abrupt stop empty.
func (t *Turn) Cancel(reason string) {
	t.cancelOnce.Do(func() {
		t.cancelReason.Store(reason)
		t.cancel()
		t.state.Store(StateCancelling)
	})
}

// CancelReason returns the first reason passed to Cancel, or "" if the turn has
// not been cancelled.
func (t *Turn) CancelReason() string {
	reason, _ := t.cancelReason.Load().(string)
	return reason
}

// State returns the turn's own view of the machine. Atomic load, so the audio
// callback may call it.
func (t *Turn) State() State {
	s, _ := t.state.Load().(State)
	return s
}

func (t *Turn) setState(s State) {
	t.state.Store(s)
}
