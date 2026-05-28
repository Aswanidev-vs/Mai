package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

type PatternType string

const (
	PatternTimeOfDay   PatternType = "time_of_day"
	PatternDayOfWeek   PatternType = "day_of_week"
	PatternSequence    PatternType = "sequence"
	PatternFrequency   PatternType = "frequency"
	PatternConditional PatternType = "conditional"
)

type UserPattern struct {
	Type       PatternType `json:"type"`
	Action     string      `json:"action"`
	Trigger    string      `json:"trigger"`
	Count      int         `json:"count"`
	LastSeen   time.Time   `json:"last_seen"`
	Confidence float64     `json:"confidence"`
	Context    string      `json:"context"`
}

type ProactiveEvent struct {
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Priority  int       `json:"priority"`
	Timestamp time.Time `json:"timestamp"`
	Context   string    `json:"context"`
}

type ProactiveEngine struct {
	mu           sync.RWMutex
	patterns     []UserPattern
	eventQueue   []ProactiveEvent
	userModel    *UserModel
	memory       *memoryManager
	lastPatterns []string
	enabled      bool
}

type memoryManager interface {
	Working() interfaces.WorkingMemory
}

func NewProactiveEngine(userModel *UserModel) *ProactiveEngine {
	return &ProactiveEngine{
		patterns: make([]UserPattern, 0),
		eventQueue: make([]ProactiveEvent, 0),
		userModel: userModel,
		enabled:   true,
	}
}

func (pe *ProactiveEngine) SetMemoryManager(mem *memoryManager) {
	pe.memory = mem
}

func (pe *ProactiveEngine) RecordAction(action string, context string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	now := time.Now()
	timeOfDay := pe.getTimeOfDay(now)

	for i, p := range pe.patterns {
		if p.Action == action && p.Trigger == timeOfDay {
			pe.patterns[i].Count++
			pe.patterns[i].LastSeen = now
			pe.patterns[i].Confidence = pe.calculateConfidence(pe.patterns[i].Count)
			return
		}
	}

	pe.patterns = append(pe.patterns, UserPattern{
		Type:       PatternTimeOfDay,
		Action:     action,
		Trigger:    timeOfDay,
		Count:      1,
		LastSeen:   now,
		Confidence: 0.1,
		Context:    context,
	})
}

func (pe *ProactiveEngine) AnalyzePatterns() []ProactiveEvent {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	if !pe.enabled {
		return nil
	}

	var events []ProactiveEvent
	now := time.Now()
	timeOfDay := pe.getTimeOfDay(now)

	for _, pattern := range pe.patterns {
		if pattern.Count >= 3 && pattern.Confidence > 0.6 && pattern.Trigger == timeOfDay {
			if time.Since(pattern.LastSeen) < 24*time.Hour {
				events = append(events, ProactiveEvent{
					Type:      "pattern_suggestion",
					Message:   fmt.Sprintf("You usually %s around this time. Shall I prepare?", pattern.Action),
					Priority:  2,
					Timestamp: now,
					Context:   pattern.Context,
				})
			}
		}
	}

	events = append(events, pe.checkSystemEvents()...)

	return events
}

func (pe *ProactiveEngine) checkSystemEvents() []ProactiveEvent {
	var events []ProactiveEvent
	now := time.Now()

	if pe.userModel != nil {
		profile := pe.userModel.GetProfile()
		if profile.TotalInteractions > 0 && time.Since(profile.LastSeen) > 30*time.Minute {
			if now.Hour() >= 9 && now.Hour() <= 22 {
				events = append(events, ProactiveEvent{
					Type:      "idle_reminder",
					Message:   "I notice you've been away. Anything I can help with?",
					Priority:  1,
					Timestamp: now,
				})
			}
		}
	}

	return events
}

func (pe *ProactiveEngine) GetPredictedActions(currentContext string) []string {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	var predictions []string
	now := time.Now()
	timeOfDay := pe.getTimeOfDay(now)

	for _, pattern := range pe.patterns {
		if pattern.Trigger == timeOfDay && pattern.Count >= 2 {
			if pe.contextMatches(currentContext, pattern.Context) {
				predictions = append(predictions, pattern.Action)
			}
		}
	}

	return predictions
}

func (pe *ProactiveEngine) GetPendingEvents() []ProactiveEvent {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	events := pe.eventQueue
	pe.eventQueue = nil
	return events
}

func (pe *ProactiveEngine) contextMatches(current, pattern string) bool {
	if current == "" || pattern == "" {
		return true
	}

	currentWords := strings.Fields(strings.ToLower(current))
	patternWords := strings.Fields(strings.ToLower(pattern))

	matches := 0
	for _, cw := range currentWords {
		for _, pw := range patternWords {
			if cw == pw {
				matches++
				break
			}
		}
	}

	return matches > 0
}

func (pe *ProactiveEngine) calculateConfidence(count int) float64 {
	if count >= 10 {
		return 0.95
	}
	if count >= 5 {
		return 0.8
	}
	if count >= 3 {
		return 0.6
	}
	return float64(count) * 0.15
}

func (pe *ProactiveEngine) getTimeOfDay(t time.Time) string {
	hour := t.Hour()
	switch {
	case hour >= 5 && hour < 12:
		return "morning"
	case hour >= 12 && hour < 17:
		return "afternoon"
	case hour >= 17 && hour < 21:
		return "evening"
	default:
		return "night"
	}
}

func (pe *ProactiveEngine) GenerateProactiveMessage(ctx context.Context, event ProactiveEvent) string {
	return event.Message
}

func (pe *ProactiveEngine) GetPatternStats() string {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	if len(pe.patterns) == 0 {
		return "No patterns recorded yet."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Tracking %d patterns:\n", len(pe.patterns)))

	for _, p := range pe.patterns {
		if p.Count >= 2 {
			sb.WriteString(fmt.Sprintf("  - %s (%s): %d times, confidence %.0f%%\n",
				p.Action, p.Trigger, p.Count, p.Confidence*100))
		}
	}

	return sb.String()
}
