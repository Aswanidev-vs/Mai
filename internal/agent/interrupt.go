package agent

import (
	"log"
	"strings"
	"sync"
)

type InterruptLevel int

const (
	InterruptLow      InterruptLevel = 0
	InterruptNormal   InterruptLevel = 1
	InterruptHigh     InterruptLevel = 2
	InterruptCritical InterruptLevel = 3
)

type InterruptRequest struct {
	Level    InterruptLevel
	Source   string
	Message  string
	Callback func()
}

type InterruptManager struct {
	mu             sync.RWMutex
	currentLevel   InterruptLevel
	isSpeaking     bool
	isProcessing   bool
	queue          []InterruptRequest
	onInterrupt    func(message string)
	onQueueProcess func(message string)
}

func NewInterruptManager() *InterruptManager {
	return &InterruptManager{
		currentLevel: InterruptLow,
		queue:        make([]InterruptRequest, 0),
	}
}

func (im *InterruptManager) SetCallbacks(onInterrupt func(string), onQueueProcess func(string)) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.onInterrupt = onInterrupt
	im.onQueueProcess = onQueueProcess
}

// SetState pushes the authoritative pipeline flags in one call. The audio
// pipeline owns them (it knows when playback actually starts and stops), so
// this is the entry point cmd/mai uses from its speaking/processing
// transitions.
func (im *InterruptManager) SetState(speaking, processing bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.isSpeaking = speaking
	im.isProcessing = processing
}

// SetSpeaking and SetProcessing update one flag at a time, so a turn that only
// knows about one side of the pipeline cannot clobber the other.
func (im *InterruptManager) SetSpeaking(speaking bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.isSpeaking = speaking
}

func (im *InterruptManager) SetProcessing(processing bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.isProcessing = processing
}

func (im *InterruptManager) State() (speaking, processing bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.isSpeaking, im.isProcessing
}

// InterruptPolicy is how an interruption is carried out, per
// DUPLEX_IMPLEMENTATION_PLAN.md §8.4.
type InterruptPolicy int

const (
	// PolicySoft finishes the sentence in flight, then stops generation.
	PolicySoft InterruptPolicy = iota
	// PolicyHard stops playback and cancels generation immediately.
	PolicyHard
	// PolicyProviderManaged leaves cancellation and truncation to the realtime
	// session, which owns its own response lifecycle.
	PolicyProviderManaged
)

// PolicyFor maps a classified utterance onto an interruption policy. The
// level alone decides: HIGH and above is the existing hard barge-in gate,
// which the plan keeps as the safe default, and everything below it is a
// backchannel or short correction.
func PolicyFor(level InterruptLevel, providerManaged bool) InterruptPolicy {
	switch {
	case providerManaged:
		return PolicyProviderManaged
	case level >= InterruptHigh:
		return PolicyHard
	default:
		return PolicySoft
	}
}

func (im *InterruptManager) CanInterrupt(level InterruptLevel) bool {
	im.mu.RLock()
	defer im.mu.RUnlock()

	if level >= InterruptCritical {
		return true
	}

	if im.isSpeaking && level >= InterruptHigh {
		return true
	}

	if im.isProcessing && level >= InterruptHigh {
		return true
	}

	if !im.isSpeaking && !im.isProcessing {
		return true
	}

	return level > im.currentLevel
}

func (im *InterruptManager) RequestInterrupt(req InterruptRequest) bool {
	im.mu.Lock()
	var notify func(string)
	accepted := true
	fireCallback := false
	switch {
	case req.Level >= InterruptCritical:
		log.Printf("[Interrupt] CRITICAL interrupt from %s: %s", req.Source, req.Message)
		im.currentLevel = req.Level
		notify = im.onInterrupt
		fireCallback = true
	case req.Level >= InterruptHigh && (im.isSpeaking || im.isProcessing):
		log.Printf("[Interrupt] HIGH interrupt from %s: %s", req.Source, req.Message)
		im.currentLevel = req.Level
		notify = im.onInterrupt
	case im.isSpeaking || im.isProcessing:
		log.Printf("[Interrupt] Queued %s interrupt from %s", levelName(req.Level), req.Source)
		im.queue = append(im.queue, req)
		accepted = false
	default:
		log.Printf("[Interrupt] Accepted %s interrupt from %s", levelName(req.Level), req.Source)
		im.currentLevel = req.Level
	}
	im.mu.Unlock()

	// Fired outside the lock: onInterrupt cancels the turn and publishes
	// agent.interrupt, which can come straight back here through Speak().
	if fireCallback && req.Callback != nil {
		req.Callback()
	}
	if notify != nil {
		notify(req.Message)
	}
	return accepted
}

func (im *InterruptManager) ProcessQueue() {
	im.mu.Lock()
	if len(im.queue) == 0 || im.isSpeaking || im.isProcessing {
		im.mu.Unlock()
		return
	}

	highest := 0
	for i, req := range im.queue {
		if req.Level > im.queue[highest].Level {
			highest = i
		}
	}

	req := im.queue[highest]
	im.queue = append(im.queue[:highest], im.queue[highest+1:]...)
	notify := im.onQueueProcess
	im.mu.Unlock()

	log.Printf("[Interrupt] Processing queued interrupt from %s: %s", req.Source, req.Message)
	if notify != nil {
		notify(req.Message)
	}
}

func (im *InterruptManager) ClearQueue() {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.queue = nil
}

func (im *InterruptManager) Reset() {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.currentLevel = InterruptLow
	im.isSpeaking = false
	im.isProcessing = false
}

func (im *InterruptManager) GetQueueSize() int {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return len(im.queue)
}

func levelName(level InterruptLevel) string {
	switch level {
	case InterruptLow:
		return "LOW"
	case InterruptNormal:
		return "NORMAL"
	case InterruptHigh:
		return "HIGH"
	case InterruptCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

func ClassifyInterrupt(message string) InterruptLevel {
	lower := strings.ToLower(message)

	urgentKeywords := []string{"emergency", "urgent", "critical", "immediately", "now", "help me", "danger"}
	for _, kw := range urgentKeywords {
		if strings.Contains(lower, kw) {
			return InterruptCritical
		}
	}

	highKeywords := []string{"important", "asap", "priority", "stop", "cancel", "wait"}
	for _, kw := range highKeywords {
		if strings.Contains(lower, kw) {
			return InterruptHigh
		}
	}

	return InterruptNormal
}
