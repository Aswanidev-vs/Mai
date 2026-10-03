package main

import (
	"sync"
	"testing"
)

// Temporary probe: does -race actually instrument this toolchain?
func TestRaceSanityProbe(t *testing.T) {
	x := make([]float32, 1024)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			for k := range x {
				x[k] += 1
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			clear(x)
		}
	}()
	wg.Wait()
}
