package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/goleak"

	"github.com/user/mai/internal/personality"
)

func TestTakeSentenceKeepsEllipsisTogether(t *testing.T) {
	var buf strings.Builder
	buf.WriteString("Hmm... That makes sense.")

	first, ok := takeSentence(&buf)
	assert.True(t, ok)
	assert.Equal(t, "Hmm...", first)

	second, ok := takeSentence(&buf)
	assert.True(t, ok)
	assert.Equal(t, "That makes sense.", second)
}

func TestTakeSentenceNaturalClauses(t *testing.T) {
	var buf strings.Builder
	// Should not split at "relax or do" / "chores."
	buf.WriteString("You're asking me this at 2:40 PM on Sunday when most people are probably trying to relax or do chores. It makes you wonder.")

	first, ok := takeSentence(&buf)
	assert.True(t, ok)
	assert.Equal(t, "You're asking me this at 2:40 PM on Sunday when most people are probably trying to relax or do chores.", first)

	second, ok := takeSentence(&buf)
	assert.True(t, ok)
	assert.Equal(t, "It makes you wonder.", second)
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestIsEcho(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		spoken  string
		isEcho  bool
	}{
		// True echoes
		{
			name:   "exact match same case",
			input:  "The weather is nice today",
			spoken: "The weather is nice today",
			isEcho: true,
		},
		{
			name:   "subset of spoken",
			input:  "weather is nice",
			spoken: "The weather is nice today and tomorrow",
			isEcho: true,
		},
		{
			name:   "same words different order mostly",
			input:  "is the weather nice today",
			spoken: "the weather is nice today",
			isEcho: true,
		},
		{
			name:   "high overlap",
			input:  "open notepad please",
			spoken: "open notepad please right now",
			isEcho: true,
		},

		// False positives (should NOT be echoes)
		{
			name:   "single word yes",
			input:  "yes",
			spoken: "The weather is nice today. Yes, it is.",
			isEcho: false,
		},
		{
			name:   "single word no",
			input:  "no",
			spoken: "Do you want coffee? No thanks.",
			isEcho: false,
		},
		{
			name:   "single word stop",
			input:  "stop",
			spoken: "Stop talking about the weather.",
			isEcho: false,
		},
		{
			name:   "two words not echo",
			input:  "thank you",
			spoken: "Thank you for your help with the weather report.",
			isEcho: false,
		},
		{
			name:   "different sentence same topic",
			input:  "what about tomorrow",
			spoken: "The weather is nice today",
			isEcho: false,
		},
		{
			name:   "low overlap",
			input:  "I like pizza",
			spoken: "The weather is nice today",
			isEcho: false,
		},
		{
			name:   "input longer than spoken",
			input:  "the weather is nice today and I love it",
			spoken: "the weather is nice",
			isEcho: false,
		},
		{
			name:   "completely different",
			input:  "hello how are you",
			spoken: "goodbye see you later",
			isEcho: false,
		},
		{
			name:   "empty input",
			input:  "",
			spoken: "something",
			isEcho: false,
		},
		{
			name:   "empty spoken",
			input:  "something",
			spoken: "",
			isEcho: false,
		},
		{
			name:   "both empty",
			input:  "",
			spoken: "",
			isEcho: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEcho(tt.input, tt.spoken)
			assert.Equal(t, tt.isEcho, result)
		})
	}
}

func TestIsEchoStrict(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		spoken  string
		isEcho  bool
	}{
		// Strict mode catches partial echoes (40% threshold)
		{
			name:   "exact match",
			input:  "the weather is nice",
			spoken: "the weather is nice today",
			isEcho: true,
		},
		{
			name:   "partial echo 50 percent",
			input:  "weather is nice",
			spoken: "the weather is nice today and tomorrow",
			isEcho: true,
		},
		{
			name:   "partial echo 40 percent",
			input:   "open notepad",
			spoken:  "please open notepad for me right now",
			isEcho:  true,
		},
		{
			name:   "low overlap not echo",
			input:  "hello world",
			spoken: "the weather is nice today",
			isEcho: false,
		},
		{
			name:   "empty input",
			input:  "",
			spoken: "something",
			isEcho: false,
		},
		{
			name:   "empty spoken",
			input:  "something",
			spoken: "",
			isEcho: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEchoStrict(tt.input, tt.spoken)
			assert.Equal(t, tt.isEcho, result)
		})
	}
}

func TestCleanResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "clean text",
			input:    "Hello, how can I help?",
			expected: "Hello, how can I help?",
		},
		{
			name:     "strip thought tags",
			input:    "<thought>I should help</thought>Here's the answer.",
			expected: "I should helpHere's the answer.",
		},
		{
			name:     "strip markdown fences",
			input:    "```json\n{ \"key\": \"value\" }\n```",
			expected: "{ \"key\": \"value\" }",
		},
		{
			name:     "strip action marker",
			input:    "I'll open that for you.[ACTION]open notepad",
			expected: "I'll open that for you.",
		},
		{
			name:     "strip preamble here is",
			input:    "Here is the response:\nThe answer is 42.",
			expected: "The answer is 42.",
		},
		{
			name:     "strip preamble heres the",
			input:    "Here's the response:\nDone.",
			expected: "Done.",
		},
		{
			name:     "strip preamble heres what",
			input:    "Here's what I found:\nThe result is positive.",
			expected: "The result is positive.",
		},
		{
			name:     "collapse triple newlines",
			input:    "Line 1\n\n\nLine 2",
			expected: "Line 1\n\nLine 2",
		},
		{
			name:     "strip leading trailing whitespace",
			input:    "  Hello world  ",
			expected: "Hello world",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only whitespace",
			input:    "   \n\n  ",
			expected: "",
		},
		{
			name:     "multiple tags",
			input:    "<thinking>step 1</thinking><thinking>step 2</thinking>Final answer.",
			expected: "step 1step 2Final answer.",
		},
		{
			name:     "strip here is json preamble",
			input:    "Here is the JSON:\n{\"key\": \"value\"}",
			expected: "{\"key\": \"value\"}",
		},
		{
			name:     "strip this is preamble",
			input:    "Here is the response:\nSuccess.",
			expected: "Success.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanResponse(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStripHallucinationHedging(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "strip im not sure but",
			input:    "I'm not sure, but the answer is 42.",
			expected: "the answer is 42.",
		},
		{
			name:     "strip i think maybe",
			input:    "I think maybe this works.",
			expected: "this works.",
		},
		{
			name:     "strip i believe perhaps",
			input:    "I believe perhaps this is correct.",
			expected: "this is correct.",
		},
		{
			name:     "strip its possible that",
			input:    "It's possible that the answer is yes.",
			expected: "the answer is yes.",
		},
		{
			name:     "strip it seems like",
			input:    "It seems like everything is fine.",
			expected: "everything is fine.",
		},
		{
			name:     "strip im not certain but",
			input:    "I'm not certain, but this should work.",
			expected: "this should work.",
		},
		{
			name:     "strip dont have confirmed",
			input:    "I don't have confirmed information, but try restarting.",
			expected: "try restarting.",
		},
		{
			name:     "strip according to training",
			input:    "According to my training data, Go is statically typed.",
			expected: "Go is statically typed.",
		},
		{
			name:     "strip as far as i know",
			input:    "As far as I know, this is true.",
			expected: "this is true.",
		},
		{
			name:     "strip if i recall correctly",
			input:    "If I recall correctly, the answer is 5.",
			expected: "the answer is 5.",
		},
		{
			name:     "no hedging",
			input:    "The answer is definitely 42.",
			expected: "The answer is definitely 42.",
		},
		{
			name:     "only first occurrence stripped",
			input:    "I'm not sure, but I'm not sure again.",
			expected: "I'm not sure again.",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "mixed case not matched",
			input:    "i'm not sure but this works",
			expected: "i'm not sure but this works",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripHallucinationHedging(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsEcho_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		spoken string
		isEcho bool
	}{
		{
			name:   "single char input",
			input:  "a",
			spoken: "a b c d e",
			isEcho: false, // 1 word, excluded
		},
		{
			name:   "two words high overlap",
			input:  "hello world",
			spoken: "hello world foo bar",
			isEcho: true, // 2 words, 100% overlap > 60%
		},
		{
			name:   "input same length as spoken",
			input:  "the cat sat on the mat",
			spoken: "the cat sat on the mat",
			isEcho: true,
		},
		{
			name:   "input longer than spoken",
			input:  "the cat sat on the mat and the dog",
			spoken: "the cat sat on the mat",
			isEcho: false,
		},
		{
			name:   "partial overlap 65 percent",
			input:  "the cat sat on the floor",
			spoken: "the cat sat on the mat and the dog",
			isEcho: true, // 5/6 = 83% > 60%
		},
		{
			name:   "partial overlap 50 percent",
			input:  "the cat and dog",
			spoken: "the cat sat on the mat",
			isEcho: false, // 2/5 = 40% < 60%
		},
		{
			name:   "spoken is subset of input",
			input:  "the cat sat on the mat and more",
			spoken: "the cat sat on the mat",
			isEcho: false, // input longer than spoken
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEcho(tt.input, tt.spoken)
			assert.Equal(t, tt.isEcho, result)
		})
	}
}

func TestIsEchoStrict_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		spoken string
		isEcho bool
	}{
		{
			name:   "very low overlap",
			input:  "foo bar baz",
			spoken: "the quick brown fox jumps",
			isEcho: false, // 0% overlap < 40%
		},
		{
			name:   "40 percent overlap",
			input:  "the cat",
			spoken: "the cat sat on the mat",
			isEcho: true, // 2/2 = 100% > 40%
		},
		{
			name:   "single word match",
			input:  "cat",
			spoken: "the cat sat on the mat",
			isEcho: true, // 1/1 = 100% > 40%
		},
		{
			name:   "empty both",
			input:  "",
			spoken: "",
			isEcho: false,
		},
		{
			name:   "long input partial match",
			input:  "the weather is nice today and tomorrow",
			spoken: "the weather is nice",
			isEcho: true, // 4/8 = 50% > 40%
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEchoStrict(tt.input, tt.spoken)
			assert.Equal(t, tt.isEcho, result)
		})
	}
}

func TestIsEcho_Performance(t *testing.T) {
	// Verify isEcho handles large inputs without excessive time
	spoken := "the quick brown fox jumps over the lazy dog "
	for i := 0; i < 10; i++ {
		spoken += spoken
	}
	input := "the quick brown fox"

	for i := 0; i < 1000; i++ {
		_ = isEcho(input, spoken)
	}
}

func TestIsEchoStrict_Performance(t *testing.T) {
	spoken := "the quick brown fox jumps over the lazy dog "
	for i := 0; i < 10; i++ {
		spoken += spoken
	}
	input := "the quick brown fox"

	for i := 0; i < 1000; i++ {
		_ = isEchoStrict(input, spoken)
	}
}

// ttsEvent is one delivery from the orchestrator to the TTS player.
type ttsEvent struct {
	text  string
	seq   int64
	final bool
}

// TestPublishTTSDropsUnspeakableFragments pins the guard on the fragment class a
// drifting model produces: takeSentence deliberately keeps "..." together as one
// segment, stripFormattingForVoice leaves punctuation intact, and Pocket answers
// a lone "..." with its 40 s maximum (measured ~30 s of CPU, most of it silence
// glitched with short bursts). The fragment must not reach the engine, and must
// not open a turn either — the player would then hold a turn open waiting for a
// sentence that was never spoken.
func TestPublishTTSDropsUnspeakableFragments(t *testing.T) {
	o := &Orchestrator{
		emotion:    personality.NewEmotionDetector(),
		ttsAdapter: personality.NewTTSAdapter(1.25, 1, 1),
	}
	var got []ttsEvent
	o.TTSFunc = func(text string, _ personality.TTSParams, seq int64, final bool) {
		got = append(got, ttsEvent{text: text, seq: seq, final: final})
	}

	for _, fragment := range []string{"...", ".", "-", "…", "—", "?!", "   "} {
		o.publishTTS(fragment)
	}
	assert.Empty(t, got, "an unpronounceable fragment must not reach the engine")

	// Speakable text still goes through, in a turn of its own.
	o.publishTTS("Right.")
	o.endTTSTurn()
	if !assert.Len(t, got, 2) {
		return
	}
	assert.Equal(t, "Right.", got[0].text)
	assert.False(t, got[0].final)
	assert.True(t, got[1].final, "the speakable sentence still closes its turn")
	assert.Equal(t, got[0].seq, got[1].seq)
}

// transcript, the voice and the viseme clock on one timeline: every sentence of
// a reply shares a single turn id, and the turn is closed by exactly one
// explicit end-of-turn marker instead of the player inferring the end of the
// answer from a momentarily empty queue.
func TestTTSTurnLifecycle(t *testing.T) {
	o := &Orchestrator{
		emotion:    personality.NewEmotionDetector(),
		ttsAdapter: personality.NewTTSAdapter(1.25, 1, 1),
	}
	var got []ttsEvent
	o.TTSFunc = func(text string, _ personality.TTSParams, seq int64, final bool) {
		got = append(got, ttsEvent{text: text, seq: seq, final: final})
	}

	o.publishTTS("First sentence.")
	o.publishTTS("Second sentence.")
	o.publishTTS("Third sentence.")
	o.endTTSTurn()

	if !assert.Len(t, got, 4) {
		return
	}
	reply := got[0].seq
	for i, e := range got[:3] {
		assert.Equal(t, reply, e.seq, "sentence %d must share the reply's turn id", i)
		assert.False(t, e.final, "sentence %d must not close the turn", i)
	}
	assert.True(t, got[3].final, "the reply must end with a final marker")
	assert.Empty(t, got[3].text, "the marker carries no text")
	assert.Equal(t, reply, got[3].seq, "the marker must close this reply's turn")

	// Idempotent, so every exit path may close the turn without double-signalling.
	o.endTTSTurn()
	assert.Len(t, got, 4, "closing a closed turn must not emit another marker")

	// A self-contained utterance is published and closed in one call.
	o.Speak("Standalone utterance.")
	if !assert.Len(t, got, 6) {
		return
	}
	assert.Equal(t, "Standalone utterance.", got[4].text)
	assert.True(t, got[5].final)
	assert.Greater(t, got[4].seq, reply, "a later reply must claim a higher turn id")

	// The next reply starts a fresh turn rather than reusing the closed one.
	o.publishTTS("Next reply.")
	o.endTTSTurn()
	if !assert.Len(t, got, 8) {
		return
	}
	assert.Greater(t, got[6].seq, got[4].seq, "a new reply must claim a new turn id")
	assert.Equal(t, got[6].seq, got[7].seq)
	assert.True(t, got[7].final)
}

