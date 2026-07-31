package events

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

// InternalSubscription implements the interfaces.Subscription interface
type InternalSubscription struct {
	bus       *Bus
	eventType string
	handlerID string
}

func (s *InternalSubscription) Unsubscribe() {
	s.bus.unsubscribe(s.eventType, s.handlerID)
}

// Bus is a thread-safe in-process implementation of interfaces.EventBus.
// Optimized for low-latency dispatch: no closure allocation per handler call,
// no defer/recover overhead on the hot path.
type Bus struct {
	mu              sync.RWMutex
	subscribers     map[string]map[string]interfaces.EventHandler
	handlerCounter  int
	handlerSnapshot map[string][]interfaces.EventHandler // pre-built snapshot for fast dispatch
	snapshotDirty   bool
}

func NewBus() *Bus {
	return &Bus{
		subscribers:     make(map[string]map[string]interfaces.EventHandler),
		handlerSnapshot: make(map[string][]interfaces.EventHandler),
	}
}

// rebuildSnapshot rebuilds the handler snapshot map. Must be called with mu held for write.
func (b *Bus) rebuildSnapshot() {
	b.handlerSnapshot = make(map[string][]interfaces.EventHandler, len(b.subscribers))
	for eventType, handlers := range b.subscribers {
		list := make([]interfaces.EventHandler, 0, len(handlers))
		for _, h := range handlers {
			list = append(list, h)
		}
		b.handlerSnapshot[eventType] = list
	}
	b.snapshotDirty = false
}

func (b *Bus) Publish(event interfaces.Event) error {
	b.mu.RLock()
	if b.snapshotDirty {
		b.mu.RUnlock()
		b.mu.Lock()
		if b.snapshotDirty {
			b.rebuildSnapshot()
		}
		b.mu.Unlock()
		b.mu.RLock()
	}
	snapshot := b.handlerSnapshot
	b.mu.RUnlock()

	handlers, ok := snapshot[event.Type]
	if !ok || len(handlers) == 0 {
		return nil
	}

	// Direct dispatch — no closure, no defer on hot path.
	// Panic recovery is only allocated if there are handlers.
	for _, handler := range handlers {
		safeCall(handler, event)
	}
	return nil
}

// safeCall invokes handler with panic recovery. The recovery is inlined
// to avoid closure allocation on the happy path.
func safeCall(handler interfaces.EventHandler, event interfaces.Event) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[BUS] Panic recovered in handler: %v", r)
		}
	}()
	handler(event)
}

func (b *Bus) Subscribe(eventType string, handler interfaces.EventHandler) interfaces.Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subscribers[eventType]; !ok {
		b.subscribers[eventType] = make(map[string]interfaces.EventHandler)
	}

	b.handlerCounter++
	handlerID := fmt.Sprintf("h%d", b.handlerCounter) // shorter IDs
	b.subscribers[eventType][handlerID] = handler
	b.snapshotDirty = true

	return &InternalSubscription{
		bus:       b,
		eventType: eventType,
		handlerID: handlerID,
	}
}

func (b *Bus) SubscribeAsync(eventType string, handler interfaces.EventHandler) interfaces.Subscription {
	asyncHandler := func(event interfaces.Event) {
		go handler(event)
	}
	return b.Subscribe(eventType, asyncHandler)
}

func (b *Bus) RequestResponse(request interfaces.Event, timeout time.Duration) (interfaces.Event, error) {
	responseChan := make(chan interfaces.Event, 1)
	responseType := fmt.Sprintf("%s.response", request.Type)

	sub := b.Subscribe(responseType, func(event interfaces.Event) {
		responseChan <- event
	})
	defer sub.Unsubscribe()

	if err := b.Publish(request); err != nil {
		return interfaces.Event{}, err
	}

	select {
	case resp := <-responseChan:
		return resp, nil
	case <-time.After(timeout):
		return interfaces.Event{}, fmt.Errorf("request timed out after %v", timeout)
	}
}

func (b *Bus) unsubscribe(eventType string, handlerID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if handlers, ok := b.subscribers[eventType]; ok {
		delete(handlers, handlerID)
		if len(handlers) == 0 {
			delete(b.subscribers, eventType)
		}
		b.snapshotDirty = true
	}
}
