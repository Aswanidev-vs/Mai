package observability

import (
	"sync"
	"testing"
	"time"
)

func TestDefaultHelpers_CounterAndGauge(t *testing.T) {
	ResetForTest()
	Incr("turns")
	Incr("turns")
	Set("active_sessions", 3)

	snap := Snapshot()
	if got := snap["turns"]; got != float64(2) {
		t.Fatalf("counter = %v, want 2", got)
	}
	if got := snap["active_sessions"]; got != float64(3) {
		t.Fatalf("gauge = %v, want 3", got)
	}
}

func TestDefaultHelpers_StagePercentiles(t *testing.T) {
	ResetForTest()
	for i := 1; i <= 10; i++ {
		RecordStage("asr", time.Duration(i)*10*time.Millisecond)
	}

	if got := StagePercentile("asr", 0.5); got != 50 {
		t.Fatalf("p50 = %v, want 50", got)
	}
	if got := StagePercentile("asr", 0.95); got != 90 {
		t.Fatalf("p95 = %v, want 90", got)
	}
	if got := StagePercentile("tts", 0.5); got != 0 {
		t.Fatalf("missing stage p50 = %v, want 0", got)
	}

	snap := Snapshot()
	if got := snap["latency_ms{operation=asr}_p50"]; got != float64(50) {
		t.Fatalf("snapshot p50 = %v, want 50", got)
	}
	if got := snap["latency_ms{operation=asr}"]; got != float64(55) {
		t.Fatalf("snapshot mean = %v, want 55", got)
	}
}

func TestDefaultHelpers_Concurrent(t *testing.T) {
	ResetForTest()
	const workers, perWorker = 16, 100

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				Incr("concurrent")
				RecordStage("llm", time.Millisecond)
			}
			_ = Snapshot()
		}()
	}
	wg.Wait()

	if got := Snapshot()["concurrent"]; got != float64(workers*perWorker) {
		t.Fatalf("counter = %v, want %d", got, workers*perWorker)
	}
	if got := StagePercentile("llm", 0.99); got != 1 {
		t.Fatalf("p99 = %v, want 1", got)
	}
}

func TestResetForTest_IsolatesTests(t *testing.T) {
	Incr("from_previous_test")
	ResetForTest()
	if _, ok := Snapshot()["from_previous_test"]; ok {
		t.Fatal("ResetForTest left stale metrics behind")
	}
}
