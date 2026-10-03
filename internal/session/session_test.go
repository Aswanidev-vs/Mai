package session

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTurnIDsAreMonotonic(t *testing.T) {
	m := NewManager()

	var prevID, prevGen uint64
	for i := 0; i < 100; i++ {
		turn := m.BeginTurn(KindUserSpeech)
		require.Equal(t, uint64(i+1), turn.ID, "turn IDs must be dense and monotonic")
		require.Greater(t, turn.ID, prevID)
		// The generation moves with the turn, so audio minted under an earlier
		// turn can never be mistaken for audio minted under this one.
		require.Greater(t, turn.GenerationID, prevGen)
		prevID, prevGen = turn.ID, turn.GenerationID
	}

	require.Equal(t, prevID, m.Current().ID, "the last turn minted is the current one")
}

func TestBeginTurnCancelsPredecessorWithReason(t *testing.T) {
	m := NewManager()
	first := m.BeginTurn(KindWakeWord)
	second := m.BeginTurn(KindBrowserMic)

	require.ErrorIs(t, first.Ctx.Err(), context.Canceled)
	require.Contains(t, first.CancelReason(), "turn 2")
	require.NoError(t, second.Ctx.Err(), "the new turn must still be live")
	require.Same(t, second, m.Current(), "the successor must be visible to a predecessor's cancellation")
}

func TestSupersedeInvalidatesAudio(t *testing.T) {
	m := NewManager()
	turn := m.BeginTurn(KindUserSpeech)

	require.False(t, m.IsStale(turn.ID, m.Generation()), "audio minted now is fresh")

	gen := m.Supersede("user barged in")
	require.Equal(t, uint64(2), gen, "Supersede returns the new generation")
	require.True(t, m.IsStale(turn.ID, gen), "the superseded turn itself is stale")
	require.True(t, m.IsStale(turn.ID, 1), "audio minted before the bump is stale")
	require.True(t, m.IsStale(turn.ID+1, gen), "a turn this manager never minted is stale too")

	require.ErrorIs(t, turn.Ctx.Err(), context.Canceled,
		"superseding a generation means the work behind it is void")
	require.Equal(t, "user barged in", turn.CancelReason())
}

func TestIdleRetiresTurn(t *testing.T) {
	m := NewManager()
	turn := m.BeginTurn(KindUserSpeech)

	require.NoError(t, m.SetState(StateListening))
	require.NoError(t, m.SetState(StateSpeaking))
	require.NoError(t, m.SetState(StateIdle))

	require.Nil(t, m.Current(), "idle means no current turn")
	require.True(t, m.IsStale(turn.ID, m.Generation()),
		"trailing audio from a finished turn must not play")
}

func TestTurnCancelIsIdempotent(t *testing.T) {
	m := NewManager()
	turn := m.BeginTurn(KindUserSpeech)

	// The barge-in path and the audio callback can both reach for the same
	// turn at the same instant; neither may lose a race or panic.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			turn.Cancel(fmt.Sprintf("caller %d", i))
		}(i)
	}
	wg.Wait()

	require.ErrorIs(t, turn.Ctx.Err(), context.Canceled)
	require.Equal(t, StateCancelling, turn.State())

	// Which caller won is nondeterministic, but the reason must not move
	// afterwards: it names what actually killed the turn.
	winner := turn.CancelReason()
	require.NotEmpty(t, winner)
	turn.Cancel("a much later reason")
	require.Equal(t, winner, turn.CancelReason())
}

func TestSupersedeOfIdleManagerIsSafe(t *testing.T) {
	// A Manager is usable before NewManager has run, and must not report a
	// state the WebSocket bridge would render as garbage.
	var zero Manager
	require.Equal(t, StateIdle, zero.State())

	m := NewManager()

	require.Equal(t, uint64(1), m.Supersede("nothing playing yet"))
	require.Nil(t, m.Current())
	require.False(t, m.ObserveOverlap(), "there is no assistant audio to overlap with")
	require.Equal(t, StateIdle, m.State())
}

// TestConcurrentManagerUse hammers every entry point from many goroutines at
// once. Its only assertion is that it finishes; the value is under -race, where
// it fails loudly if any hot-path read escapes an atomic.
func TestConcurrentManagerUse(t *testing.T) {
	m := NewManager()

	const goroutines = 8
	const iterations = 300

	var wg sync.WaitGroup
	var maxGen atomic.Uint64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				switch (g + i) % 7 {
				case 0:
					turn := m.BeginTurn(KindUserSpeech)
					m.SetState(StateListening)
					m.IsStale(turn.ID, m.Generation())
				case 1:
					m.Supersede("concurrent supersede")
				case 2:
					if cur := m.Current(); cur != nil {
						cur.Cancel("concurrent cancel")
					}
				case 3:
					m.SetState(StateSpeaking)
					m.ObserveOverlap()
				case 4:
					m.Overlap()
					m.State()
				case 5:
					m.SetState(StateCancelling)
					m.SetState(StateIdle)
				case 6:
					gen := m.Generation()
					for {
						old := maxGen.Load()
						if gen <= old || maxGen.CompareAndSwap(old, gen) {
							break
						}
					}
				}
			}
		}(g)
	}

	wg.Wait()
	require.Positive(t, m.Generation())
}

func TestSetStateAcceptsLegalPaths(t *testing.T) {
	paths := [][]State{
		{StateIdle, StateListening},
		{StateIdle, StateListening, StateSpeaking, StateBoth, StateCancelling, StateIdle},
		// The assistant loses the floor when the user talks over her.
		{StateIdle, StateListening, StateSpeaking, StateBoth, StateListening},
		{StateIdle, StateFailed, StateIdle},
	}

	for _, path := range paths {
		m := NewManager()
		m.BeginTurn(KindUserSpeech)
		for _, s := range path {
			require.NoErrorf(t, m.SetState(s), "%s -> %s", path, s)
			require.Equal(t, s, m.State())
		}
	}
}

func TestSetStateRejectsIllegalTransitions(t *testing.T) {
	tests := []struct {
		name string
		path []State
		to   State
	}{
		{"idle cannot start speaking with no turn", []State{StateIdle}, StateSpeaking},
		{"idle has no audio to overlap with", []State{StateIdle}, StateBoth},
		{"idle cannot start cancelling", []State{StateIdle}, StateCancelling},
		{"a cancelled turn cannot resume speaking", []State{StateIdle, StateListening, StateSpeaking, StateCancelling}, StateSpeaking},
		{"a cancelled turn cannot resume listening", []State{StateIdle, StateListening, StateSpeaking, StateCancelling}, StateListening},
		{"a cancelled turn cannot jump back to both", []State{StateIdle, StateListening, StateSpeaking, StateCancelling}, StateBoth},
		{"a failed turn cannot resume speaking", []State{StateIdle, StateFailed}, StateSpeaking},
		{"a failed turn cannot resume listening", []State{StateIdle, StateFailed}, StateListening},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager()
			m.BeginTurn(KindUserSpeech)
			for _, s := range tc.path {
				require.NoError(t, m.SetState(s), "test path must be legal")
			}
			before := m.State()

			err := m.SetState(tc.to)

			require.Error(t, err)
			require.ErrorIs(t, err, ErrIllegalTransition)
			// Rejected, not applied: a stale goroutine must not be able to
			// put the machine back into speaking.
			assert.Equal(t, before, m.State())
		})
	}
}

func TestSetStateAllowsIdempotentRestate(t *testing.T) {
	m := NewManager()
	m.BeginTurn(KindUserSpeech)

	// Two goroutines learn about the same transition independently; neither
	// should see an error for reporting it.
	require.NoError(t, m.SetState(StateListening))
	require.NoError(t, m.SetState(StateListening))
	require.NoError(t, m.SetState(StateSpeaking))
	require.NoError(t, m.SetState(StateSpeaking))
	require.Equal(t, StateSpeaking, m.State())
}

func TestObserveOverlapRecordsEpisodes(t *testing.T) {
	m := NewManager()
	m.BeginTurn(KindUserSpeech)

	require.False(t, m.ObserveOverlap(), "idle has no assistant audio")
	require.NoError(t, m.SetState(StateListening))
	require.False(t, m.ObserveOverlap(), "listening is not overlapping")

	require.NoError(t, m.SetState(StateSpeaking))
	before := time.Now()
	require.True(t, m.ObserveOverlap())
	assert.Equal(t, StateBoth, m.State())
	require.False(t, m.ObserveOverlap(), "already inside the overlap")

	time.Sleep(20 * time.Millisecond)
	require.NoError(t, m.SetState(StateSpeaking))

	stats := m.Overlap()
	assert.EqualValues(t, 1, stats.Count)
	assert.GreaterOrEqual(t, stats.Total, 20*time.Millisecond)
	assert.GreaterOrEqual(t, stats.Longest, 20*time.Millisecond)
	assert.False(t, stats.FirstAt.Before(before))
	assert.False(t, stats.LastAt.Before(stats.FirstAt))
}

// TestClassify locks in the distinctions barge-in currently cannot make. Every
// row is an observation the live path can actually produce: the levels come from
// the measured room (residual 0.0844 was a reported false trigger) and the
// coherence figures from the measured echo-path gap.
func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		in   Features
		want AudioClass
	}{
		{
			// Mai's leaked voice after warmup, at the residual level that was
			// actually reported cuting her off.
			name: "echo tail during playback is her own voice",
			in:   Features{ResidualRMS: 0.0844, Coherence: 0.81, Speech: true, Overlap: 300 * time.Millisecond, AssistantSpeaking: true},
			want: ClassEcho,
		},
		{
			name: "echo exactly at the ceiling is still echo",
			in:   Features{ResidualRMS: 0.05, Coherence: EchoCoherenceMax, Speech: true, AssistantSpeaking: true},
			want: ClassEcho,
		},
		{
			name: "echo wins even when the frame is quiet",
			in:   Features{ResidualRMS: 0.002, Coherence: 0.95, Speech: true, AssistantSpeaking: true},
			want: ClassEcho,
		},
		{
			// A fan or HVAC drone: clears every energy gate, carries no words.
			name: "laptop fan over playback is noise",
			in:   Features{ResidualRMS: 0.06, Coherence: 0.12, Speech: false, Overlap: 200 * time.Millisecond, AssistantSpeaking: true},
			want: ClassNoise,
		},
		{
			name: "loud and unvoiced is still noise",
			in:   Features{ResidualRMS: 0.9, Coherence: 0.2, Speech: false, AssistantSpeaking: true},
			want: ClassNoise,
		},
		{
			name: "silence is noise",
			in:   Features{ResidualRMS: 0.0007, Coherence: 0, Speech: false, Overlap: 0},
			want: ClassNoise,
		},
		{
			name: "quiet speech is below the gate",
			in:   Features{ResidualRMS: ResidualGateRMS / 2, Speech: true, AssistantSpeaking: true},
			want: ClassNoise,
		},
		{
			name: "mm-hmm over her sentence is backchannel",
			in:   Features{ResidualRMS: 0.031, Coherence: 0.38, Speech: true, Overlap: 240 * time.Millisecond, AssistantSpeaking: true},
			want: ClassBackchannel,
		},
		{
			name: "backchannel exactly at the ceiling is still backchannel",
			in:   Features{ResidualRMS: 0.031, Coherence: 0.38, Speech: true, Overlap: BackchannelMaxOverlap, AssistantSpeaking: true},
			want: ClassBackchannel,
		},
		{
			name: "past the ceiling the user is carrying content",
			in:   Features{ResidualRMS: 0.031, Coherence: 0.38, Speech: true, Overlap: BackchannelMaxOverlap + time.Millisecond, AssistantSpeaking: true},
			want: ClassBargeIn,
		},
		{
			name: "a skipped coherence scan cannot read as echo",
			in:   Features{ResidualRMS: 0.031, Coherence: 0, Speech: true, Overlap: 240 * time.Millisecond, AssistantSpeaking: true},
			want: ClassBackchannel,
		},
		{
			name: "wait no over her sentence is barge-in",
			in:   Features{ResidualRMS: 0.045, Coherence: 0.41, Speech: true, Overlap: 700 * time.Millisecond, AssistantSpeaking: true},
			want: ClassBargeIn,
		},
		{
			// Overlap is zero whenever the assistant is silent — ObserveOverlap
			// only ever records a speaking→both transition — so ordinary speech
			// with Mai quiet can never read as an interruption.
			name: "the user talking with Mai silent interrupts nothing",
			in:   Features{ResidualRMS: 0.05, Coherence: 0, Speech: true, Overlap: 0},
			want: ClassBackchannel,
		},
		{
			name: "no assistant audio means coherence is never echo",
			in:   Features{ResidualRMS: 0.05, Coherence: 0.9, Speech: false, Overlap: 0},
			want: ClassNoise,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Classify(tc.in))
		})
	}
}

func TestClassifyIsDeterministic(t *testing.T) {
	f := Features{ResidualRMS: 0.031, Coherence: 0.38, Speech: true, Overlap: 240 * time.Millisecond, AssistantSpeaking: true}
	first := Classify(f)
	for i := 0; i < 100; i++ {
		require.Equal(t, first, Classify(f))
	}
}
