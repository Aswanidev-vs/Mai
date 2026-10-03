package agent

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/user/mai/pkg/interfaces"
)

func TestStatusValueDefaultsToIdle(t *testing.T) {
	o := &Orchestrator{}

	assert.Equal(t, interfaces.StatusIdle, o.GetStatus())
}

func TestStatusSetAndGet(t *testing.T) {
	o := &Orchestrator{}

	o.setStatus(interfaces.StatusThinking)
	assert.Equal(t, interfaces.StatusThinking, o.GetStatus())

	o.setStatus(interfaces.StatusIdle)
	assert.Equal(t, interfaces.StatusIdle, o.GetStatus())
}

// The status is written by HandleInput and read from other goroutines (the
// server bridge polls it on every request), so the field must stay race-free
// under concurrent access — this test fails on -race without the atomic.
func TestStatusConcurrentReadWrite(t *testing.T) {
	o := &Orchestrator{}
	statuses := []interfaces.AgentStatus{
		interfaces.StatusIdle, interfaces.StatusThinking,
		interfaces.StatusActing, interfaces.StatusListening, interfaces.StatusSpeaking,
	}

	const writers = 8
	const readers = 4
	const iterations = 500

	stop := make(chan struct{})
	var readersWG sync.WaitGroup
	for r := 0; r < readers; r++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = o.GetStatus()
				}
			}
		}()
	}

	var writersWG sync.WaitGroup
	for w := 0; w < writers; w++ {
		writersWG.Add(1)
		go func(w int) {
			defer writersWG.Done()
			for i := 0; i < iterations; i++ {
				o.setStatus(statuses[(w+i)%len(statuses)])
			}
		}(w)
	}

	writersWG.Wait()
	close(stop)
	readersWG.Wait()

	// Whatever the interleaving, the final read must be a legal status.
	assert.Contains(t, statuses, o.GetStatus())
}
