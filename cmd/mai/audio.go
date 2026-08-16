package main

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
)

// audioCapture manages microphone input via miniaudio.
type audioCapture struct {
	ctx        *malgo.AllocatedContext
	device     *malgo.Device
	onSamples  func([]float32)
	sampleRate uint32
	channels   uint32
}

// newAudioCapture initializes a microphone capture device.
func newAudioCapture(sampleRate uint32, channels uint32) *audioCapture {
	return &audioCapture{
		sampleRate: sampleRate,
		channels:   channels,
	}
}

// Start begins capturing audio from the microphone.
func (c *audioCapture) Start() error {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		fmt.Printf("[malgo] %s\n", message)
	})
	if err != nil {
		return fmt.Errorf("init context: %w", err)
	}
	c.ctx = ctx

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = c.channels
	deviceConfig.SampleRate = c.sampleRate
	deviceConfig.Alsa.NoMMap = 1

	callbacks := malgo.DeviceCallbacks{
		Data: c.onRecvFrames,
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, callbacks)
	if err != nil {
		ctx.Free()
		return fmt.Errorf("init device: %w", err)
	}
	c.device = device

	return device.Start()
}

// Stop halts audio capture.
func (c *audioCapture) Stop() error {
	if c.device != nil {
		return c.device.Stop()
	}
	return nil
}

// Close releases audio resources.
func (c *audioCapture) Close() {
	if c.device != nil {
		c.device.Uninit()
		c.device = nil
	}
	if c.ctx != nil {
		_ = c.ctx.Uninit()
		c.ctx.Free()
		c.ctx = nil
	}
}

var rawDataLogged = false

// onRecvFrames converts raw int16 bytes to float32 samples.
func (c *audioCapture) onRecvFrames(_, pSample []byte, frameCount uint32) {
	if !rawDataLogged && len(pSample) > 0 {
		fmt.Printf("\r[DEBUG] First raw audio packet received: %d bytes\n", len(pSample))
		rawDataLogged = true
	}
	if c.onSamples == nil {
		return
	}
	n := len(pSample) / 2
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		s16 := int16(pSample[2*i]) | int16(pSample[2*i+1])<<8
		samples[i] = float32(s16) / 32768.0
	}
	c.onSamples(samples)
}

// playAudioStreaming plays TTS chunks as they arrive on a channel.
// The generator function is called in a goroutine and should send
// float32 sample slices into the returned channel, then close it when done.
// Returns once the channel is closed and all buffered audio has been played.
func playAudioStreaming(ctx context.Context, sampleRate int, stop *int32, generate func(ch chan<- []float32)) error {
	ch := make(chan []float32, 64)

	// Run the TTS generator in a goroutine.
	go func() {
		defer close(ch)
		generate(ch)
	}()

	devCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return err
	}
	defer devCtx.Free()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = 1
	deviceConfig.SampleRate = uint32(sampleRate)

	var mu sync.Mutex
	var buf []float32
	var readIdx int
	var streamDone bool
	cond := sync.NewCond(&mu)

	rs := newResampler(44100, 16000) // TTS is played at 44.1k; reference must match the 16k mic.
	onSamples := func(pOutputSample, _ []byte, frameCount uint32) {
		if stop != nil && atomic.LoadInt32(stop) != 0 {
			return
		}
		mu.Lock()
		start := readIdx
		n := int(frameCount)
		written := 0
		for written < n {
			if readIdx >= len(buf) {
				if streamDone {
					break
				}
				// Wait for new data instead of busy-spinning
				cond.Wait()
				continue
			}
			s := buf[readIdx]
			readIdx++
			s16 := int16(s * 32767.0)
			pOutputSample[written*2] = byte(s16 & 0xFF)
			pOutputSample[written*2+1] = byte(s16 >> 8)
			written++
		}
		// Feed exactly the samples sent to the speaker into the echo reference.
		if written > 0 {
			refBuffer.Push(rs.resample(buf[start:readIdx]))
		}
		// Trim consumed data periodically to avoid unbounded growth.
		if readIdx > 8192 && readIdx > len(buf)/2 {
			buf = buf[readIdx:]
			readIdx = 0
		}
		mu.Unlock()
	}

	device, err := malgo.InitDevice(devCtx.Context, deviceConfig, malgo.DeviceCallbacks{Data: onSamples})
	if err != nil {
		return err
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		return err
	}

	// Drain channel into the shared buffer.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for chunk := range ch {
			if stop != nil && atomic.LoadInt32(stop) != 0 {
				return
			}
			mu.Lock()
			buf = append(buf, chunk...)
			cond.Signal() // Wake up the playback callback
			mu.Unlock()
		}
		mu.Lock()
		streamDone = true
		cond.Broadcast() // Wake up all waiters so they can see streamDone
		mu.Unlock()
	}()

	// Wait for generation to finish.
	<-done

	// Drain remaining channel content to unblock the generator goroutine.
	// When stop is set mid-stream, the drain goroutine exits early, leaving
	// the generator blocked on ch <- chunk (channel full). Consuming the
	// remaining chunks lets the generator finish and close the channel.
	for range ch {
	}

	// Wait for playback to drain.
	for {
		mu.Lock()
		remaining := len(buf) - readIdx
		mu.Unlock()
		if remaining <= 0 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if stop != nil && atomic.LoadInt32(stop) != 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}

	return nil
}

// playAudio plays float32 samples through the default output device.
// It can be interrupted midway via ctx cancellation or the stop flag.
func playAudio(ctx context.Context, samples []float32, sampleRate int, stop *int32) error {
	devCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return err
	}
	defer devCtx.Free()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = 1
	deviceConfig.SampleRate = uint32(sampleRate)

	var playbackIndex int
	onSamples := func(pOutputSample, _ []byte, frameCount uint32) {
		if stop != nil && atomic.LoadInt32(stop) != 0 {
			return // Stop playback immediately on barge-in
		}
		n := int(frameCount)
		start := playbackIndex
		for i := 0; i < n; i++ {
			if playbackIndex >= len(samples) {
				return
			}
			s16 := int16(samples[playbackIndex] * 32767.0)
			pOutputSample[i*2] = byte(s16 & 0xFF)
			pOutputSample[i*2+1] = byte(s16 >> 8)
			playbackIndex++
		}
		// Chime also leaves the speaker, so include it in the echo reference.
		if playbackIndex > start {
			refBuffer.Push(samples[start:playbackIndex])
		}
	}

	device, err := malgo.InitDevice(devCtx.Context, deviceConfig, malgo.DeviceCallbacks{Data: onSamples})
	if err != nil {
		return err
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		return err
	}

	// Poll loop with context awareness and stop flag support
	for playbackIndex < len(samples) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if stop != nil && atomic.LoadInt32(stop) != 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}

	return nil
}

// generateThinkingChime creates a short 440Hz sine wave tone for the thinking indicator.
// Duration ~80ms, amplitude ~-12dB (0.25) to be subtle.
func generateThinkingChime(sampleRate int) []float32 {
	duration := 0.08 // 80ms
	numSamples := int(float64(sampleRate) * duration)
	samples := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		// Sine wave at 440Hz with a quick fade-out envelope
		envelope := 1.0 - float64(i)/float64(numSamples)
		samples[i] = float32(0.25 * math.Sin(2*math.Pi*440*t) * envelope)
	}
	return samples
}
