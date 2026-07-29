package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
