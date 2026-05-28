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
	mu              sync.RWMutex
	currentLevel    InterruptLevel
	isSpeaking      bool
	isProcessing    bool
	queue           []InterruptRequest
	onInterrupt     func(message string)
	onQueueProcess  func(message string)
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

func (im *InterruptManager) SetState(speaking, processing bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.isSpeaking = speaking
	im.isProcessing = processing
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
	defer im.mu.Unlock()

	if req.Level >= InterruptCritical {
		log.Printf("[Interrupt] CRITICAL interrupt from %s: %s", req.Source, req.Message)
		im.currentLevel = req.Level
		if req.Callback != nil {
			req.Callback()
		}
		if im.onInterrupt != nil {
			im.onInterrupt(req.Message)
		}
		return true
	}

	if req.Level >= InterruptHigh && (im.isSpeaking || im.isProcessing) {
		log.Printf("[Interrupt] HIGH interrupt from %s: %s", req.Source, req.Message)
		im.currentLevel = req.Level
		if im.onInterrupt != nil {
			im.onInterrupt(req.Message)
		}
		return true
	}

	if !im.isSpeaking && !im.isProcessing {
		log.Printf("[Interrupt] Accepted %s interrupt from %s", levelName(req.Level), req.Source)
		im.currentLevel = req.Level
		return true
	}

	log.Printf("[Interrupt] Queued %s interrupt from %s", levelName(req.Level), req.Source)
	im.queue = append(im.queue, req)
	return false
}

func (im *InterruptManager) ProcessQueue() {
	im.mu.Lock()
	defer im.mu.Unlock()

	if len(im.queue) == 0 || im.isSpeaking || im.isProcessing {
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

	log.Printf("[Interrupt] Processing queued interrupt from %s: %s", req.Source, req.Message)
	if im.onQueueProcess != nil {
		im.onQueueProcess(req.Message)
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
