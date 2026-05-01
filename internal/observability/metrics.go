package observability

import (
	"log"
	"sync"
	"time"
)

type MetricType string

const (
	Counter   MetricType = "counter"
	Gauge     MetricType = "gauge"
	Histogram MetricType = "histogram"
)

type Metric struct {
	Name      string     `json:"name"`
	Type      MetricType `json:"type"`
	Value     float64    `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time  `json:"timestamp"`
}

type MetricsCollector struct {
	mu      sync.RWMutex
	metrics map[string]*Metric
	windows map[string][]timedValue
}

type timedValue struct {
	value     float64
	timestamp time.Time
}

func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		metrics: make(map[string]*Metric),
		windows: make(map[string][]timedValue),
	}
}

func (mc *MetricsCollector) IncrCounter(name string, labels map[string]string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	key := metricKey(name, labels)
	if m, ok := mc.metrics[key]; ok {
		m.Value++
		m.Timestamp = time.Now()
	} else {
		mc.metrics[key] = &Metric{
			Name: name, Type: Counter, Value: 1,
			Labels: labels, Timestamp: time.Now(),
		}
	}
}

func (mc *MetricsCollector) SetGauge(name string, value float64, labels map[string]string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	key := metricKey(name, labels)
	mc.metrics[key] = &Metric{
		Name: name, Type: Gauge, Value: value,
		Labels: labels, Timestamp: time.Now(),
	}
}

func (mc *MetricsCollector) RecordHistogram(name string, value float64, labels map[string]string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	key := metricKey(name, labels)
	mc.windows[key] = append(mc.windows[key], timedValue{value: value, timestamp: time.Now()})

	// Keep last 1000 values
	if len(mc.windows[key]) > 1000 {
		mc.windows[key] = mc.windows[key][len(mc.windows[key])-1000:]
	}

	// Update gauge to average
	var sum float64
	for _, v := range mc.windows[key] {
		sum += v.value
	}
	mc.metrics[key] = &Metric{
		Name: name, Type: Histogram, Value: sum / float64(len(mc.windows[key])),
		Labels: labels, Timestamp: time.Now(),
	}
}

func (mc *MetricsCollector) RecordLatency(operation string, duration time.Duration) {
	mc.RecordHistogram("latency_ms", float64(duration.Milliseconds()), map[string]string{"operation": operation})
}

func (mc *MetricsCollector) GetMetric(name string, labels map[string]string) *Metric {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.metrics[metricKey(name, labels)]
}

func (mc *MetricsCollector) GetAll() []Metric {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	var result []Metric
	for _, m := range mc.metrics {
		result = append(result, *m)
	}
	return result
}

func (mc *MetricsCollector) Percentile(name string, labels map[string]string, p float64) float64 {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	key := metricKey(name, labels)
	values := mc.windows[key]
	if len(values) == 0 {
		return 0
	}

	sorted := make([]float64, len(values))
	for i, v := range values {
		sorted[i] = v.value
	}

	// Simple insertion sort (fine for <1000 values)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

func metricKey(name string, labels map[string]string) string {
	key := name
	for k, v := range labels {
		key += "|" + k + "=" + v
	}
	return key
}

type StructuredLogger struct {
	component string
}

func NewStructuredLogger(component string) *StructuredLogger {
	return &StructuredLogger{component: component}
}

func (l *StructuredLogger) Info(event string, fields ...interface{}) {
	log.Printf("[%s] %s %v", l.component, event, fields)
}

func (l *StructuredLogger) Warn(event string, fields ...interface{}) {
	log.Printf("[%s] WARN %s %v", l.component, event, fields)
}

func (l *StructuredLogger) Error(event string, fields ...interface{}) {
	log.Printf("[%s] ERROR %s %v", l.component, event, fields)
}

func (l *StructuredLogger) Debug(event string, fields ...interface{}) {
	log.Printf("[%s] DEBUG %s %v", l.component, event, fields)
}

type HealthChecker struct {
	mu       sync.RWMutex
	checks   map[string]func() error
	statuses map[string]string
}

func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		checks:   make(map[string]func() error),
		statuses: make(map[string]string),
	}
}

func (hc *HealthChecker) RegisterCheck(name string, checkFn func() error) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.checks[name] = checkFn
	hc.statuses[name] = "unknown"
}

func (hc *HealthChecker) RunChecks() map[string]string {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	for name, checkFn := range hc.checks {
		if err := checkFn(); err != nil {
			hc.statuses[name] = "unhealthy: " + err.Error()
		} else {
			hc.statuses[name] = "healthy"
		}
	}

	result := make(map[string]string)
	for k, v := range hc.statuses {
		result[k] = v
	}
	return result
}

func (hc *HealthChecker) IsHealthy() bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	for _, status := range hc.statuses {
		if status != "healthy" && status != "unknown" {
			return false
		}
	}
	return true
}
