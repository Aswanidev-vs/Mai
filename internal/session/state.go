package session

import (
	"errors"
	"fmt"

	"github.com/user/mai/pkg/interfaces"
)

// State is the duplex state machine's position: whose audio is in the air
// right now.
//
// It is an alias of interfaces.AgentStatus rather than a parallel enum because
// that vocabulary already exists and is already on the wire (StatusSpeaking and
// StatusListening are defined but were never assigned anywhere before this
// package). Aliasing means Manager.State is a drop-in for the
// getStatus func() interfaces.AgentStatus that internal/server.NewBridge wants —
// no adapter, and no second spelling of "speaking" for the UI to disagree with.
// Only the three states with no wire equivalent are added here.
type State = interfaces.AgentStatus

const (
	StateIdle      = interfaces.StatusIdle
	StateListening = interfaces.StatusListening
	StateSpeaking  = interfaces.StatusSpeaking

	// StateBoth is the true-duplex state: the user is speaking and the
	// assistant is still talking over them. Nothing in the wire vocabulary
	// means this, so it has no interfaces counterpart.
	StateBoth State = "both"

	// StateCancelling means the turn was told to die and is unwinding. It is
	// the gate that keeps a half-cancelled turn from resuming output: TTS
	// chunks arriving here are already stale, and moving straight back to
	// speaking would mean an interrupted answer resumed itself.
	StateCancelling State = "cancelling"

	// StateFailed means the turn died rather than ended — provider error,
	// dropped device. Distinct from idle because it must not be reported as a
	// clean close.
	StateFailed State = "failed"
)

// ErrIllegalTransition is wrapped by every rejected SetState, so callers that
// care can match it with errors.Is instead of parsing the message.
var ErrIllegalTransition = errors.New("illegal duplex state transition")

// legalTransitions is the duplex state machine.
//
// Two asymmetries are deliberate:
//
//   - idle cannot reach speaking/both directly. There is no audio to speak or
//     to overlap with until a user turn has opened, so a report of speaking
//     with no turn is a stale goroutine talking to a manager that has moved on,
//     and dropping it is what keeps a late-arriving TTS chunk from putting the
//     machine back into speaking.
//   - cancelling can only leave for idle or failed. The turn it was cancelling
//     may still be unwinding an LLM stream; resuming work here is exactly the
//     "assistant talks over the user anyway" failure barge-in exists to stop.
//
// Re-reporting the state a machine is already in is always legal: several
// goroutines legitimately learn about the same transition independently.
var legalTransitions = map[State][]State{
	StateIdle:       {StateListening, StateFailed},
	StateListening:  {StateSpeaking, StateBoth, StateCancelling, StateIdle, StateFailed},
	StateSpeaking:   {StateBoth, StateCancelling, StateIdle, StateFailed},
	StateBoth:       {StateSpeaking, StateListening, StateCancelling, StateIdle, StateFailed},
	StateCancelling: {StateIdle, StateFailed},
	StateFailed:     {StateIdle},
}

func legalTransition(from, to State) bool {
	if from == to {
		return true
	}
	for _, s := range legalTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

func transitionError(from, to State) error {
	return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
}
