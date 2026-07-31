package events

import (
	"testing"

	"github.com/user/mai/pkg/interfaces"
)

func BenchmarkBus_Publish(b *testing.B) {
	bus := NewBus()
	handler := func(event interfaces.Event) {}
	bus.Subscribe("test.event", handler)

	event := interfaces.Event{Type: "test.event", Payload: map[string]interface{}{"key": "value"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Publish(event)
	}
}

func BenchmarkBus_Publish10Handlers(b *testing.B) {
	bus := NewBus()
	for i := 0; i < 10; i++ {
		bus.Subscribe("test.event", func(event interfaces.Event) {})
	}

	event := interfaces.Event{Type: "test.event", Payload: map[string]interface{}{"key": "value"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Publish(event)
	}
}

func BenchmarkBus_Subscribe(b *testing.B) {
	bus := NewBus()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Subscribe("test.event", func(event interfaces.Event) {})
	}
}
