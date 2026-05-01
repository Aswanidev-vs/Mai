package agent

import (
	"fmt"
	"log"
	"sync"
	"time"
)

type OperationMetrics struct {
	Count       int           `json:"count"`
	TotalTime   time.Duration `json:"total_time"`
	MinTime     time.Duration `json:"min_time"`
	MaxTime     time.Duration `json:"max_time"`
	LastTime    time.Duration `json:"last_time"`
	Failures    int           `json:"failures"`
	SuccessRate float64       `json:"success_rate"`
}

type PerformanceMetrics struct {
	mu         sync.RWMutex
	operations map[string]*OperationMetrics
	actions    struct {
		success int
		fail    int
	}
	startTime time.Time
}

func NewPerformanceMetrics() *PerformanceMetrics {
	return &PerformanceMetrics{
		operations: make(map[string]*OperationMetrics),
		startTime:  time.Now(),
	}
}

func (pm *PerformanceMetrics) RecordLatency(operation string, duration time.Duration) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	op, ok := pm.operations[operation]
	if !ok {
		op = &OperationMetrics{MinTime: duration}
		pm.operations[operation] = op
	}

	op.Count++
	op.TotalTime += duration
	op.LastTime = duration
	if duration < op.MinTime {
		op.MinTime = duration
	}
	if duration > op.MaxTime {
		op.MaxTime = duration
	}
}

func (pm *PerformanceMetrics) RecordActionResult(success bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if success {
		pm.actions.success++
	} else {
		pm.actions.fail++
	}
}

func (pm *PerformanceMetrics) RecordFailure(operation string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if op, ok := pm.operations[operation]; ok {
		op.Failures++
	}
}

func (pm *PerformanceMetrics) GetReport() MetricsReport {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	report := MetricsReport{
		Uptime:      time.Since(pm.startTime),
		Operations:  make(map[string]OperationMetrics),
	}

	total := pm.actions.success + pm.actions.fail
	if total > 0 {
		report.ActionSuccessRate = float64(pm.actions.success) / float64(total)
	}
	report.TotalActions = total

	for name, op := range pm.operations {
		metrics := *op
		if op.Count > 0 {
			metrics.SuccessRate = float64(op.Count-op.Failures) / float64(op.Count)
		}
		report.Operations[name] = metrics
	}

	return report
}

type MetricsReport struct {
	Uptime            time.Duration                `json:"uptime"`
	TotalActions      int                          `json:"total_actions"`
	ActionSuccessRate float64                      `json:"action_success_rate"`
	Operations        map[string]OperationMetrics  `json:"operations"`
}

type MetaCognition struct {
	metrics     *PerformanceMetrics
	strategies  map[string]*StrategyRecord
	mu          sync.RWMutex
}

type StrategyRecord struct {
	Name       string    `json:"name"`
	Successes  int       `json:"successes"`
	Failures   int       `json:"failures"`
	LastUsed   time.Time `json:"last_used"`
	AvgLatency time.Duration `json:"avg_latency"`
}

func NewMetaCognition() *MetaCognition {
	return &MetaCognition{
		metrics:    NewPerformanceMetrics(),
		strategies: make(map[string]*StrategyRecord),
	}
}

func (mc *MetaCognition) RecordLatency(operation string, duration time.Duration) {
	mc.metrics.RecordLatency(operation, duration)
}

func (mc *MetaCognition) RecordActionResult(success bool) {
	mc.metrics.RecordActionResult(success)
}

func (mc *MetaCognition) RecordStrategy(strategy string, success bool, latency time.Duration) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	s, ok := mc.strategies[strategy]
	if !ok {
		s = &StrategyRecord{Name: strategy}
		mc.strategies[strategy] = s
	}

	if success {
		s.Successes++
	} else {
		s.Failures++
	}
	s.LastUsed = time.Now()

	total := s.Successes + s.Failures
	if total > 0 {
		s.AvgLatency = (s.AvgLatency*time.Duration(total-1) + latency) / time.Duration(total)
	}
}

func (mc *MetaCognition) AnalyzeStrategy() string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	report := mc.metrics.GetReport()

	if report.TotalActions < 5 {
		return "Insufficient data for analysis. Continue current approach."
	}

	if report.ActionSuccessRate < 0.5 {
		log.Printf("[Meta] Low success rate: %.1f%% — suggesting strategy change", report.ActionSuccessRate*100)
		return "Success rate is below 50%. Consider: 1) Using different tools, 2) Breaking tasks into smaller steps, 3) Asking user for clarification."
	}

	if report.ActionSuccessRate > 0.9 {
		return "Excellent performance. Current strategy is effective."
	}

	// Find slowest operation
	var slowest string
	var slowestTime time.Duration
	for name, op := range report.Operations {
		if op.Count > 0 {
			avg := op.TotalTime / time.Duration(op.Count)
			if avg > slowestTime {
				slowestTime = avg
				slowest = name
			}
		}
	}

	if slowest != "" && slowestTime > 10*time.Second {
		return "Bottleneck detected: " + slowest + " averaging " + slowestTime.String() + ". Consider optimizing or using an alternative approach."
	}

	return "Performance is acceptable. Success rate: " + formatPercent(report.ActionSuccessRate)
}

func (mc *MetaCognition) GetBestStrategy(taskType string) string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	var best string
	var bestRate float64 = -1

	for name, s := range mc.strategies {
		total := s.Successes + s.Failures
		if total < 2 {
			continue
		}
		rate := float64(s.Successes) / float64(total)
		if rate > bestRate {
			bestRate = rate
			best = name
		}
	}

	return best
}

func (mc *MetaCognition) GetReport() MetricsReport {
	return mc.metrics.GetReport()
}

func formatPercent(v float64) string {
	return fmt.Sprintf("%.1f%%", v*100)
}
