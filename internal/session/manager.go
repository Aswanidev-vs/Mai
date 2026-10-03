// Package session gives the voice pipeline one owner for turn identity and the
// duplex state machine.
//
// Before it existed, "whose turn is it?" had no answer: isSpeaking, ttsPlaying,
// stopPlayback, latestTurn and ttsSeq were five separate atomics owned by five
// different goroutines, so there was nowhere to hang a cancel func and no way to
// tell echo from barge-in from a fan. Manager is that owner — input workers,
// the LLM stream, the synthesiser and the playback device all ask it the same
// two questions ("is my work still current?" and "whose audio is this?") and get
// one consistent answer.
//
// Concurrency shape: every hot-path read is a plain atomic load (Current, State,
// Generation, IsStale) and the single mutex guards only turn installation and
// state-transition validation — a handful of field assignments with no I/O and
// no allocation inside. Nothing here sends on a channel or waits on another
// subsystem, because the miniaudio capture callback runs inside this package's
// callers' budget.
package session

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Manager owns the current turn, the assistant-audio generation counter and the
// duplex state. All methods are safe for concurrent use from the audio callback,
// the WebSocket goroutine, the LLM-stream goroutine and the TTS player
// goroutines simultaneously.
type Manager struct {
	nextTurnID atomic.Uint64
	generation atomic.Uint64

	// cur is nil while idle. Turn records are immutable-after-mint apart from
	// their atomic state, so a reader can hold the pointer across calls.
	cur   atomic.Pointer[Turn]
	state atomic.Value // State

	mu      sync.Mutex
	overlap overlap
}

// NewManager returns a Manager in StateIdle with no turn open.
func NewManager() *Manager {
	m := &Manager{}
	m.state.Store(StateIdle)
	return m
}

// Current returns the open turn, or nil when idle. Lock-free.
func (m *Manager) Current() *Turn {
	return m.cur.Load()
}

// BeginTurn mints a turn, opens it as the current one and cancels whatever was
// running. Cancellation reason names the successor so a log line about a stalled
// LLM stream still says which turn displaced it.
//
// It deliberately does not touch the machine state: barge-in wants a new turn
// that cancels the old one, backchannel wants the same turn kept alive, and only
// the caller knows which of the two just happened. A caller that has opened a
// turn still calls SetState.
//
// The predecessor is cancelled after the new turn is installed and with the lock
// released, so a predecessor's own reaction to cancellation already sees the new
// turn via Current(), and an LLM stream that calls straight back into the
// manager cannot deadlock against the audio callback waiting here.
func (m *Manager) BeginTurn(kind Kind) *Turn {
	ctx, cancel := context.WithCancel(context.Background())
	turn := newTurn(m.nextTurnID.Add(1), m.generation.Add(1), kind, ctx, cancel)

	m.mu.Lock()
	prev := m.cur.Load()
	m.cur.Store(turn)
	m.mu.Unlock()

	if prev != nil {
		prev.Cancel(fmt.Sprintf("superseded by turn %d", turn.ID))
	}
	return turn
}

// Supersede declares every piece of assistant audio minted before this call
// dead, and returns the new generation. Synthesis jobs tag themselves with the
// generation they were minted under; playback flush and TTS cancellation learn
// they are stale from Manager.IsStale rather than from a shared flag that has to
// be cleared by hand.
//
// The current turn is cancelled with the same reason, because that is what a
// superseded generation means: the work behind it is void. Leaving the context
// alive would let an in-flight LLM stream keep producing tokens that IsStale
// discards forever — the "keeps burning CPU and bleeds into the next turn"
// failure.
func (m *Manager) Supersede(reason string) uint64 {
	gen := m.generation.Add(1)
	if cur := m.cur.Load(); cur != nil {
		cur.Cancel(reason)
	}
	return gen
}

// Generation returns the current assistant-audio generation. Tag every
// synthesis job with the value read after its turn was minted — Turn.GenerationID
// is the generation the turn opened in, which a later Supersede has already
// moved past.
func (m *Manager) Generation() uint64 {
	return m.generation.Load()
}

// IsStale reports whether work tagged with this turn and generation has been
// superseded. This is the hot check the TTS chunk path runs per chunk: three
// atomic loads, no allocation and no lock.
//
// A chunk is fresh only when it carries exactly the generation the manager is on
// now, and its turn is still one that can produce audio. With no current turn at
// all nothing is fresh — that is what keeps a finished turn's trailing audio from
// playing after the machine went idle — and neither can a turn that is already
// unwinding, since re-tagging a dead turn's audio under the current generation is
// how Mai would talk over the user anyway.
func (m *Manager) IsStale(turnID, generation uint64) bool {
	cur := m.cur.Load()
	if cur == nil {
		return true
	}
	if cur.ID != turnID || generation != m.generation.Load() {
		return true
	}
	switch cur.State() {
	case StateCancelling, StateFailed:
		return true
	}
	return false
}

// State returns the machine's current position. Lock-free: this replaces the
// isSpeaking load on the audio path.
func (m *Manager) State() State {
	s, ok := m.state.Load().(State)
	if !ok {
		return StateIdle
	}
	return s
}

// SetState moves the machine, rejecting illegal transitions with an error and a
// log line. It never panics: a rejected transition is a race between two
// goroutines reporting the same moment, and the loser of that race is normal
// operation, not a programming error.
//
// Entering StateIdle retires the current turn, so Current returns nil again and
// any audio still tagged with that turn becomes stale.
func (m *Manager) SetState(s State) error {
	m.mu.Lock()
	err := m.setStateLocked(s)
	m.mu.Unlock()

	if err != nil {
		// Outside the lock: logging can block on stderr, and the audio
		// callback is one of the callers that may end up here.
		log.Printf("[SESSION] %v", err)
	}
	return err
}

func (m *Manager) setStateLocked(s State) error {
	from := m.State()
	if !legalTransition(from, s) {
		return transitionError(from, s)
	}
	if from == s {
		return nil
	}

	// start and finish are only ever called from setStateLocked, which always
	// starts an episode on the way into StateBoth — so an episode in progress is
	// exactly a machine sitting in StateBoth, and finish never sees an empty one.
	now := time.Now()
	if from == StateBoth {
		m.overlap.finish(now)
	}
	if s == StateBoth {
		m.overlap.start(now)
	}

	m.state.Store(s)
	if cur := m.cur.Load(); cur != nil {
		cur.setState(s)
	}
	if s == StateIdle {
		m.cur.Store(nil)
	}
	return nil
}

// ObserveOverlap records that the user started talking over the assistant, which
// enters StateBoth, and reports whether a new overlap episode actually began.
//
// It is data collection, not a decision: today every burst that clears the
// barge-in gate is treated as a full interruption, so "mm-hmm" and "wait, no"
// cancel the assistant identically. Wave 3 needs the distribution of overlap
// lengths to tell them apart, and it cannot be reconstructed afterwards.
//
// Only speaking→both counts. A user talking while Mai is silent is ordinary
// turn-taking, not overlap, and while idle there is no assistant audio for an
// overlap to be measured against.
func (m *Manager) ObserveOverlap() bool {
	if m.State() != StateSpeaking {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.State() != StateSpeaking {
		return false
	}
	return m.setStateLocked(StateBoth) == nil
}

// OverlapStats summarises the overlap episodes observed so far. Total and
// Longest only include episodes that have finished: an overlap still in progress
// is not counted until the machine leaves StateBoth.
type OverlapStats struct {
	Count   uint64
	Total   time.Duration
	Longest time.Duration
	FirstAt time.Time
	LastAt  time.Time
}

// Overlap returns a snapshot of the overlap counters. Takes the lock, so it
// belongs on a metrics or reporting path rather than the audio callback.
func (m *Manager) Overlap() OverlapStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return OverlapStats{
		Count:   m.overlap.count,
		Total:   m.overlap.total,
		Longest: m.overlap.longest,
		FirstAt: m.overlap.firstAt,
		LastAt:  m.overlap.lastAt,
	}
}

type overlap struct {
	count     uint64
	total     time.Duration
	longest   time.Duration
	firstAt   time.Time
	lastAt    time.Time
	startedAt time.Time // zero unless an episode is in progress
}

func (o *overlap) start(now time.Time) {
	o.count++
	if o.firstAt.IsZero() {
		o.firstAt = now
	}
	o.lastAt = now
	o.startedAt = now
}

func (o *overlap) finish(now time.Time) {
	d := now.Sub(o.startedAt)
	o.startedAt = time.Time{}
	o.total += d
	if d > o.longest {
		o.longest = d
	}
}
