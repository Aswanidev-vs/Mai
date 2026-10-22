package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise the queue/reset contract without a model. The actual
// sherpa stream is owned by the worker; what matters to the audio path is that
// Submit is non-blocking, copies its input, and Reset starts a clean generation.
func TestStreamingASRSubmitCopiesAndDropsOldest(t *testing.T) {
	a := &streamingASR{in: make(chan asrFrame, 2)}
	a.snapshot.Store(asrSnapshot{})

	first := []float32{1, 2, 3}
	a.Submit(first)
	first[0] = 99 // the queued frame must not alias the caller's slice
	f := <-a.in
	assert.Equal(t, []float32{1, 2, 3}, f.samples, "Submit must copy the input")

	a.Submit([]float32{4, 5, 6})
	a.Submit([]float32{7, 8, 9})
	a.Submit([]float32{10, 11, 12}) // queue full: oldest is discarded, newest wins

	require.Len(t, a.in, 2)
	f1 := <-a.in
	f2 := <-a.in
	assert.Equal(t, []float32{7, 8, 9}, f1.samples)
	assert.Equal(t, []float32{10, 11, 12}, f2.samples)
	assert.Equal(t, uint64(1), a.Dropped())
}

func TestStreamingASRResetDrainsOldGeneration(t *testing.T) {
	a := &streamingASR{in: make(chan asrFrame, 2)}
	a.snapshot.Store(asrSnapshot{})

	a.Submit([]float32{1})
	a.Submit([]float32{2})
	require.Len(t, a.in, 2)

	a.Reset()
	assert.Empty(t, a.in)
	assert.Equal(t, uint64(1), a.Generation())
	assert.Equal(t, uint64(1), a.Snapshot().Generation)

	a.Submit([]float32{3})
	require.Len(t, a.in, 1)
	f := <-a.in
	assert.Equal(t, uint64(1), f.generation)
}
