package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripWakeWordPrefix(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Hey Mai, send a message to Manu on WhatsApp saying hello", "send a message to Manu on WhatsApp saying hello"},
		{"Mai send the message to Manu on WhatsApp saying hello", "send the message to Manu on WhatsApp saying hello"},
		{"Mai, open Chrome", "open Chrome"},
		{"Mai's open Chrome", "open Chrome"},
		{"Mai Mai, open Chrome", "open Chrome"},
		{"My, send a message to Manu on WhatsApp saying hello", "send a message to Manu on WhatsApp saying hello"},
		{"My send a message to Manu on WhatsApp saying hello", "send a message to Manu on WhatsApp saying hello"},
		{"My name is Aswani", "My name is Aswani"},
		{"Maybe later", "Maybe later"},
		{"maid service", "maid service"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, stripWakeWordPrefix(tt.in))
		})
	}
}

func TestDedupeRepeatedTranscript(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{
			"On WhatsApp saying hello Sunday message to Manu on WhatsApp saying hello",
			"Sunday message to Manu on WhatsApp saying hello",
		},
		{
			"send a message to Manu send a message to Manu on WhatsApp",
			"send a message to Manu on WhatsApp",
		},
		{
			"please play the song please play the song on YouTube",
			"please play the song on YouTube",
		},
		{
			"send a message to Manu on WhatsApp saying hello",
			"send a message to Manu on WhatsApp saying hello",
		},
		{
			// Removing a 5-word prefix leaves fewer than 2*n words; the
			// smaller n must skip rather than slice past the end.
			"a b c d e a b c d e x",
			"a b c d e x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, dedupeRepeatedTranscript(tt.in))
		})
	}
}

func TestIsLikelyRepetitiveTranscript(t *testing.T) {
	assert.True(t, isLikelyRepetitiveTranscript("send a message to Manu send a message to Manu send a message to Manu"))
	assert.False(t, isLikelyRepetitiveTranscript("send a message to Manu on WhatsApp saying hello"))
	assert.False(t, isLikelyRepetitiveTranscript("hello"))
}
