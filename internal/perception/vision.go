package perception

import (
	"context"
	"log"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

// VisionProcessor handles environmental vision sensing
type VisionProcessor struct {
	bus      interfaces.EventBus
	analyze  func() (string, error) // Callback to existing vision.go analysis
	interval time.Duration
}

func NewVisionProcessor(bus interfaces.EventBus, analyze func() (string, error), interval time.Duration) *VisionProcessor {
	return &VisionProcessor{
		bus:      bus,
		analyze:  analyze,
		interval: interval,
	}
}

func (v *VisionProcessor) Start(ctx context.Context) {
	ticker := time.NewTicker(v.interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				scene, err := v.analyze()
				if err != nil {
					log.Printf("[Vision] Analysis error: %v", err)
					continue
				}
				
				v.bus.Publish(interfaces.Event{
					Type:   "perception.vision.scene",
					Source: "vision.processor",
					Payload: map[string]interface{}{
						"description": scene,
					},
				})
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}
