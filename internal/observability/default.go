package observability

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Only the pointer is swappable (by ResetForTest); MetricsCollector already
// serializes its own state, so helpers never need to hold defaultMu for long.
var (
	defaultMu        sync.RWMutex
	defaultCollector = NewMetricsCollector()
)

// Default lets new call sites record metrics without plumbing a collector
// through every layer of the app.
func Default() *MetricsCollector {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultCollector
}

// ResetForTest gives each test a clean collector instead of sharing live data.
func ResetForTest() {
	defaultMu.Lock()
	defaultCollector = NewMetricsCollector()
	defaultMu.Unlock()
}

func Incr(name string) {
	Default().IncrCounter(name, nil)
}

func Set(name string, value float64) {
	Default().SetGauge(name, value, nil)
}

// RecordStage feeds stage timings into the shared latency_ms histogram.
func RecordStage(stage string, d time.Duration) {
	Default().RecordLatency(stage, d)
}

func StagePercentile(stage string, p float64) float64 {
	return Default().Percentile("latency_ms", map[string]string{"operation": stage}, p)
}

// Snapshot exposes histograms too: their values live in a separate window
// that GetAll alone does not cover.
func Snapshot() map[string]any {
	all := Default().GetAll()
	snap := make(map[string]any, len(all))
	for _, m := range all {
		key := snapshotKey(m)
		snap[key] = m.Value
		if m.Type != Histogram {
			continue
		}
		for _, p := range []float64{0.5, 0.95, 0.99} {
			snap[fmt.Sprintf("%s_p%d", key, int(p*100))] = Default().Percentile(m.Name, m.Labels, p)
		}
	}
	return snap
}

func snapshotKey(m Metric) string {
	if len(m.Labels) == 0 {
		return m.Name
	}
	labels := make([]string, 0, len(m.Labels))
	for k, v := range m.Labels {
		labels = append(labels, k+"="+v)
	}
	sort.Strings(labels) // label maps are unordered; keep snapshot keys stable
	return m.Name + "{" + strings.Join(labels, ",") + "}"
}
