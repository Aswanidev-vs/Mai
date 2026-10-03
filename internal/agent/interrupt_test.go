package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyFor(t *testing.T) {
	tests := []struct {
		name            string
		level           InterruptLevel
		providerManaged bool
		want            InterruptPolicy
	}{
		{"critical is hard", InterruptCritical, false, PolicyHard},
		{"high is hard", InterruptHigh, false, PolicyHard},
		{"normal is soft", InterruptNormal, false, PolicySoft},
		{"low is soft", InterruptLow, false, PolicySoft},
		{"provider owns critical", InterruptCritical, true, PolicyProviderManaged},
		{"provider owns high", InterruptHigh, true, PolicyProviderManaged},
		{"provider owns normal", InterruptNormal, true, PolicyProviderManaged},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, PolicyFor(tc.level, tc.providerManaged))
		})
	}
}

// With the state unset the manager silently accepts everything, which is what
// made it inert: a HIGH interrupt arriving mid-answer must actually fire the
// cancel callback.
func TestRequestInterrupt_HighWhileSpeakingFiresCallback(t *testing.T) {
	im := NewInterruptManager()
	var got string
	im.SetCallbacks(func(msg string) { got = msg }, nil)
	im.SetState(true, false)

	accepted := im.RequestInterrupt(InterruptRequest{Level: InterruptHigh, Source: "user", Message: "stop"})

	assert.True(t, accepted)
	assert.Equal(t, "stop", got)
	assert.True(t, im.CanInterrupt(InterruptHigh))
}

func TestRequestInterrupt_QueuesWhenBusyAndNotUrgent(t *testing.T) {
	im := NewInterruptManager()
	fired := false
	im.SetCallbacks(func(string) { fired = true }, nil)
	im.SetState(true, true)

	accepted := im.RequestInterrupt(InterruptRequest{Level: InterruptNormal, Source: "user", Message: "mm-hmm"})

	assert.False(t, accepted)
	assert.False(t, fired)
	assert.Equal(t, 1, im.GetQueueSize())
}

func TestInterruptManager_SetStateAndState(t *testing.T) {
	im := NewInterruptManager()

	im.SetSpeaking(true)
	speaking, processing := im.State()
	assert.True(t, speaking)
	assert.False(t, processing)

	im.SetProcessing(true)
	im.SetSpeaking(false)
	speaking, processing = im.State()
	assert.False(t, speaking)
	assert.True(t, processing)

	im.SetState(false, false)
	speaking, processing = im.State()
	assert.False(t, speaking)
	assert.False(t, processing)
}

// Callbacks used to run while the manager held its lock, and onInterrupt
// reaches Speak() -> ttsTurnID -> SetSpeaking: re-entering the same mutex.
// The callback must now be invoked with the lock released.
func TestRequestInterrupt_CallbackMayReenterManager(t *testing.T) {
	im := NewInterruptManager()
	im.SetCallbacks(func(msg string) { im.SetState(false, false) }, nil)
	im.SetState(true, false)

	done := make(chan bool, 1)
	go func() {
		done <- im.RequestInterrupt(InterruptRequest{Level: InterruptHigh, Source: "user", Message: "stop"})
	}()

	select {
	case accepted := <-done:
		assert.True(t, accepted)
	case <-time.After(2 * time.Second):
		t.Fatal("RequestInterrupt deadlocked with a re-entrant callback")
	}
	speaking, _ := im.State()
	assert.False(t, speaking)
}

// The orchestrator must feed the manager its own turn activity; without this
// the flags stay false and every decision takes the permissive idle branch.
func TestOrchestrator_PublishesSpeakingState(t *testing.T) {
	o := &Orchestrator{interrupts: NewInterruptManager()}

	o.ttsTurnID()
	speaking, processing := o.interrupts.State()
	assert.True(t, speaking, "a spoken turn must mark the agent as speaking")
	assert.False(t, processing)

	o.endTTSTurn()
	speaking, _ = o.interrupts.State()
	assert.False(t, speaking, "closing the turn must mark the agent as silent")

	// endTTSTurn is idempotent: a second call must not resurrect state.
	o.endTTSTurn()
	speaking, _ = o.interrupts.State()
	assert.False(t, speaking)
}

func TestOrchestrator_SetPipelineState(t *testing.T) {
	o := &Orchestrator{interrupts: NewInterruptManager()}

	o.SetPipelineState(true, true)
	speaking, processing := o.interrupts.State()
	assert.True(t, speaking)
	assert.True(t, processing)
}

func TestOrchestrator_HandleInterruptHard(t *testing.T) {
	im := NewInterruptManager()
	var got string
	im.SetCallbacks(func(msg string) { got = msg }, nil)
	im.SetState(true, true)
	o := &Orchestrator{interrupts: im}

	policy := o.HandleInterrupt("stop talking", false)

	assert.Equal(t, PolicyHard, policy)
	assert.Equal(t, "stop talking", got)
	assert.False(t, o.softStop.Load())
}

func TestOrchestrator_HandleInterruptSoft(t *testing.T) {
	im := NewInterruptManager()
	im.SetCallbacks(func(string) { t.Error("soft interrupt must not fire the hard cancel callback") }, nil)
	im.SetState(true, false)
	o := &Orchestrator{interrupts: im}

	policy := o.HandleInterrupt("mm-hmm", false)

	assert.Equal(t, PolicySoft, policy)
	assert.True(t, o.softStop.Load())
	assert.Equal(t, 0, im.GetQueueSize())
}

func TestOrchestrator_HandleInterruptSoftWhileIdleIsInert(t *testing.T) {
	o := &Orchestrator{interrupts: NewInterruptManager()}

	policy := o.HandleInterrupt("mm-hmm", false)

	assert.Equal(t, PolicySoft, policy)
	assert.False(t, o.softStop.Load(), "nothing is playing, so there is nothing to stop")
}

func TestOrchestrator_HandleInterruptProviderManagedLeavesLocalStateAlone(t *testing.T) {
	im := NewInterruptManager()
	im.SetCallbacks(func(string) { t.Error("provider-managed interrupts are cancelled by the provider") }, nil)
	im.SetState(true, true)
	o := &Orchestrator{interrupts: im}

	policy := o.HandleInterrupt("stop", true)

	assert.Equal(t, PolicyProviderManaged, policy)
	assert.False(t, o.softStop.Load())
	assert.Equal(t, 0, im.GetQueueSize())
}

// The soft stop is one-shot and must reach the in-flight turn's cancel.
func TestOrchestrator_StopAtBoundaryCancelsTurnOnce(t *testing.T) {
	o := &Orchestrator{interrupts: NewInterruptManager()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o.setTurnCancel(cancel)
	o.softStop.Store(true)

	o.stopAtBoundary()
	assert.Error(t, ctx.Err(), "a soft interrupt must stop the turn in flight")
	assert.False(t, o.softStop.Load())

	o.stopAtBoundary() // second call is a no-op, not a second cancel
}

func TestClassifyInterruptLevels(t *testing.T) {
	assert.Equal(t, InterruptCritical, ClassifyInterrupt("this is an emergency"))
	assert.Equal(t, InterruptHigh, ClassifyInterrupt("cancel that"))
	assert.Equal(t, InterruptNormal, ClassifyInterrupt("what's the weather"))
}

// The interrupt manager state must survive concurrent pipeline pushes.
func TestInterruptManager_ConcurrentStateUpdates(t *testing.T) {
	im := NewInterruptManager()
	done := make(chan struct{})

	for i := 0; i < 4; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 200; j++ {
				im.SetSpeaking(i%2 == 0)
				im.SetProcessing(j%2 == 0)
				_, _ = im.State()
				im.RequestInterrupt(InterruptRequest{Level: InterruptNormal, Source: "test"})
				im.ClearQueue()
			}
		}(i)
	}

	for i := 0; i < 4; i++ {
		<-done
	}
	require.Equal(t, 0, im.GetQueueSize())
}
