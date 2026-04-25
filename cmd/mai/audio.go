package main

import (
	"fmt"
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

// playAudio plays float32 samples through the default output device.
func playAudio(samples []float32, sampleRate int) error {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return err
	}
	defer ctx.Free()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = 1
	deviceConfig.SampleRate = uint32(sampleRate)

	var playbackIndex int
	onSamples := func(pOutputSample, _ []byte, frameCount uint32) {
		n := int(frameCount)
		for i := 0; i < n; i++ {
			if playbackIndex >= len(samples) {
				return
			}
			s16 := int16(samples[playbackIndex] * 32767.0)
			pOutputSample[i*2] = byte(s16 & 0xFF)
			pOutputSample[i*2+1] = byte(s16 >> 8)
			playbackIndex++
		}
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, malgo.DeviceCallbacks{Data: onSamples})
	if err != nil {
		return err
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		return err
	}

	for playbackIndex < len(samples) {
		time.Sleep(10 * time.Millisecond)
	}

	return nil
}

