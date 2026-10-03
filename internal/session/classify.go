package session

import "time"

// AudioClass labels a burst of microphone audio relative to what the assistant
// is doing at the time.
type AudioClass string

const (
	// ClassEcho is Mai's own voice coming back through the room, not the user.
	ClassEcho AudioClass = "echo"

	// ClassNoise is audio that carries no words: room tone, a fan, HVAC, a
	// reverb tail. It must never open a turn or cancel one.
	ClassNoise AudioClass = "noise"

	// ClassBackchannel is speech that must not cancel the assistant: an
	// "mm-hmm", a "yeah" over her sentence. Overlapping without interrupting is
	// what full duplex means, and there is currently no path for it — every
	// burst that clears the gate cancels the turn.
	ClassBackchannel AudioClass = "backchannel"

	// ClassBargeIn is speech carrying content that should win over what Mai is
	// saying, so the assistant stops.
	ClassBargeIn AudioClass = "barge_in"
)

const (
	// EchoCoherenceMax is the normalised correlation between the residual and
	// the speaker reference above which the residual is her voice leaking
	// through rather than someone interrupting.
	//
	// Taken from measurement on a simulated room echo path with a fresh
	// (unconverged) canceller: Mai's own leaked voice scores 0.73-0.87 while a
	// normal interruption over her voice scores 0.45-0.67, so 0.7 sits in the
	// middle of that gap. It is the same ceiling the live barge-in gate uses
	// (cmd/mai/main.go, bargeInEchoMaxDefault) — one number, because there is
	// one physical distinction to draw.
	EchoCoherenceMax = 0.7

	// ResidualGateRMS is the residual level below which a frame cannot carry a
	// word. It matches the default barge_in_threshold, which is where the
	// measured room sits below a speaking voice.
	//
	// This is lower than the gate barge-in itself uses (threshold * 2.5).
	// Cutting Mai off needs a margin because getting it wrong is a silence she
	// did not ask for; labelling audio does not — the voiced check below still
	// has to agree before anything is treated as a person.
	ResidualGateRMS = 0.008

	// BackchannelMaxOverlap is how long the user and assistant can talk over
	// each other before the user's side stops counting as an assent.
	//
	// An assent token is one or two syllables — "mm-hmm", "yeah", "sure" — which
	// at a normal speaking pace runs about 200-350ms. Past roughly 400ms the
	// utterance has started carrying content words, and content wins over the
	// assistant. The existing barge-in sustain (150ms) is not the same
	// measurement: that is how long energy must persist before an interruption
	// is even considered, whereas this is the semantic length of an
	// interjection.
	BackchannelMaxOverlap = 400 * time.Millisecond
)

// Features is one observation of the mic. It is a struct rather than five
// positional arguments because the order is not self-evident at the call site and
// coherence in particular is easy to swap for a level.
type Features struct {
	// ResidualRMS is the level of the echo-cancelled signal. Mai's own voice is
	// loud in here too, which is why energy alone cannot decide anything.
	ResidualRMS float64

	// Coherence is the normalised correlation of the residual with the speaker
	// reference, in [0,1]. Pass 0 when the coherence scan was skipped: it costs
	// ~1.6M multiply-adds, so the live path only runs it once a cheap energy
	// gate has already fired, and 0 is below EchoCoherenceMax — a scan that
	// never ran can therefore never be mistaken for echo.
	Coherence float64

	// Speech is the noise enhancer's verdict that the residual carries words.
	// A stationary drone clears every energy gate and carries none, which is
	// what makes this the second independent check.
	Speech bool

	// Overlap is how long the user and assistant have been talking over each
	// other, measured from Manager.ObserveOverlap. Zero whenever
	// AssistantSpeaking is false: an overlap is only ever recorded from a
	// speaking→both transition, so there is nothing to measure while she is
	// silent.
	Overlap time.Duration

	// AssistantSpeaking must be true for the whole window in which Mai's audio
	// can still reach the mic — that is, the playback itself *and* the
	// post-playback AEC window that follows it. The room is still ringing after
	// the device stops, and the same echo ceiling has to reject that tail.
	AssistantSpeaking bool
}

// Classify labels one observation. It is pure and deterministic: same features,
// same class, no clocks and no I/O, so it can be table-driven in a test and
// replayed against recorded audio.
//
// The rules, in order:
//
//	assistant speaking && coherence >= EchoCoherenceMax -> Echo
//	residual below the gate, or the enhancer found no speech    -> Noise
//	speech that overlaps for at most BackchannelMaxOverlap      -> Backchannel
//	speech that overlaps for longer                             -> BargeIn
//
// Echo is tested first because it is the one that must never be missed: her own
// voice is *loud* in the residual and passes every energy gate, so an
// energy-first ordering would label her own playback as the user talking. That
// is not hypothetical — a 4096-tap canceller needs a couple of seconds of echo
// to converge, and before the coherence check existed her leaked voice tripped
// the barge-in gate every single time, at warmup plus sustain.
//
// With the assistant silent the overlap is zero by construction, so every voiced
// frame lands in Backchannel and every unvoiced one in Noise: both mean "do not
// cancel", which is the correct answer when there is nothing to cancel.
func Classify(f Features) AudioClass {
	if f.AssistantSpeaking && f.Coherence >= EchoCoherenceMax {
		return ClassEcho
	}
	if f.ResidualRMS < ResidualGateRMS || !f.Speech {
		return ClassNoise
	}
	if f.Overlap <= BackchannelMaxOverlap {
		return ClassBackchannel
	}
	return ClassBargeIn
}
