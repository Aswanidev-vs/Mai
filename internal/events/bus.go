package events

import (
	"fmt"
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

// Bus is a thread-safe in-process implementation of interfaces.EventBus
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string]map[string]interfaces.EventHandler
	handlerCounter int
}

func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[string]map[string]interfaces.EventHandler),
	}
}

func (b *Bus) Publish(event interfaces.Event) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	handlers, ok := b.subscribers[event.Type]
	if !ok {
		return nil
	}

	for _, handler := range handlers {
		handler(event)
	}
	return nil
}

func (b *Bus) Subscribe(eventType string, handler interfaces.EventHandler) interfaces.Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subscribers[eventType]; !ok {
		b.subscribers[eventType] = make(map[string]interfaces.EventHandler)
	}

	b.handlerCounter++
	handlerID := fmt.Sprintf("handler-%d", b.handlerCounter)
	b.subscribers[eventType][handlerID] = handler

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
	// Simple implementation using a temporary subscription
	responseChan := make(chan interfaces.Event, 1)
	responseType := fmt.Sprintf("%s.response", request.Type)

	sub := b.Subscribe(responseType, func(event interfaces.Event) {
		// Check if it matches the session/request ID if we had one
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
	}
}
