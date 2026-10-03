package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// bargeInSustainMs / bargeInWarmupMs mirror the defaults the task pins the
// period against. These are the tuned values; the point of the assertions
// below is that audioPeriodMillis divides both, so the period cannot silently
// retune them.
const (
	testSustainMs = 250
	testWarmupMs  = 700
)

func TestAudioPeriod_DividesBargeInConstants(t *testing.T) {
	assert.Equal(t, 0, testSustainMs%audioPeriodMillis,
		"a period that does not divide bargeInSustain quantises it: %d ms would become %d ms",
		testSustainMs, ceilToPeriod(testSustainMs))
	assert.Equal(t, 0, testWarmupMs%audioPeriodMillis,
		"a period that does not divide bargeInWarmup quantises it")
}

// ceilToPeriod is the effective duration of a wall-clock hold evaluated once
// per frame: the first frame boundary at or after the hold.
func ceilToPeriod(ms int) int {
	n := ms / audioPeriodMillis
	if ms%audioPeriodMillis != 0 {
		n++
	}
	return n * audioPeriodMillis
}

func TestAudioPeriod_CaptureRate(t *testing.T) {
	// 16 kHz is the rate newAudioCapture is called with, and the rate the AEC
	// tap window (4096 taps = 256 ms) and the DPDFNet hop (160 = 10 ms) are
	// both quoted in.
	assert.Equal(t, uint32(400), audioPeriodFrames(16000))
	assert.Equal(t, 25.0, float64(audioPeriodFrames(16000))/16.0)
}

func TestAudioPeriod_DerivesPerRate(t *testing.T) {
	// Playback runs at 44.1 kHz for TTS and 16 kHz for the chime. A hardcoded
	// frame count would be 9 ms of speaker period at 44.1 kHz, so the period
	// has to be derived.
	assert.Equal(t, uint32(1103), audioPeriodFrames(44100))
	assert.Equal(t, uint32(551), audioPeriodFrames(22050))
	assert.Equal(t, uint32(1200), audioPeriodFrames(48000))

	for _, rate := range []uint32{8000, 16000, 22050, 44100, 48000} {
		ms := float64(audioPeriodFrames(rate)) / float64(rate) * 1000
		assert.InDelta(t, audioPeriodMillis, ms, 0.03,
			"rate %d derived %.3f ms", rate, ms)
	}
}

func TestAudioPeriod_EchoWindowBudget(t *testing.T) {
	// EchoCanceller.Process aligns a queued frame against the newest L+n
	// reference samples, so audioQueueDepth frames of lag is the extra echo
	// path length the adaptive filter is asked to model. It must stay well
	// inside NewEchoCanceller(4096).
	lag := audioQueueDepth * int(audioPeriodFrames(16000))
	assert.Equal(t, 1200, lag)
	assert.Less(t, lag, 4096, "queue lag budget must fit the canceller's tap window")
	assert.Less(t, float64(lag)/4096.0, 0.5, "lag budget should stay a small fraction of the window")

	// For contrast: the period miniaudio picks when none is set is the
	// conservative default, 100 ms, which puts the budget *outside* the window.
	assert.Greater(t, audioQueueDepth*1000*16, 4096)
}

func TestAudioPeriod_FitsPlaybackStagingCap(t *testing.T) {
	// push() admits chunks in slices, but the ring still has to be able to
	// hold a whole device period or the renderer would block with the cap full
	// and the producer parked behind it.
	for _, rate := range []int{16000, 44100} {
		ring := newSampleRing(rate)
		period := int(audioPeriodFrames(uint32(rate)))
		assert.Greater(t, ring.limit, period,
			"cap at %d Hz must exceed the %d-frame period", rate, period)
		assert.GreaterOrEqual(t, ring.limit/period, 4,
			"want several periods of slack at %d Hz, got %d", rate, ring.limit/period)
	}
}

func TestAudioPeriod_ZeroRateDefersToBackend(t *testing.T) {
	assert.Equal(t, uint32(0), audioPeriodFrames(0))
}

func TestAudioPeriod_LowRateFloor(t *testing.T) {
	// A degenerate rate must not produce a period the backend can only clamp
	// back up, which would make the setting a lie.
	assert.Equal(t, uint32(minAudioPeriodFrames), audioPeriodFrames(100))
}

func TestAudioCapture_ObservedPeriod(t *testing.T) {
	// No device: onRecvFrames is driven directly, which is exactly what the
	// miniaudio thread does with the bytes it hands over.
	c := newAudioCapture(16000, 1)
	assert.Equal(t, uint32(400), c.RequestedPeriodFrames())
	assert.Equal(t, uint32(0), c.ObservedPeriodFrames(), "unmeasured is not a mismatch")
	assert.True(t, c.PeriodHonoured(), "an unobserved period must not read as rejected")

	var got []float32
	c.onSamples = func(s []float32) { got = s }

	raw := make([]byte, 400*2)
	c.onRecvFrames(nil, raw, 400)
	assert.Len(t, got, 400)
	assert.Equal(t, uint32(400), c.ObservedPeriodFrames())
	assert.True(t, c.PeriodHonoured())

	// Backend substituted a different period: this must be detectable, which
	// is the whole reason the observed value exists.
	c.onRecvFrames(nil, raw, 1024)
	assert.Equal(t, uint32(400), c.ObservedPeriodFrames(), "first observation wins")
	assert.True(t, c.PeriodHonoured())

	other := newAudioCapture(16000, 1)
	other.onRecvFrames(nil, raw, 1024)
	assert.Equal(t, uint32(1024), other.ObservedPeriodFrames())
	assert.False(t, other.PeriodHonoured(), "a substituted period must be visible")
}
