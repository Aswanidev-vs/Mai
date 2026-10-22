package main

import "strings"

// stripWakeWordPrefix removes the wake word that the streaming recognizer may
// have kept in the transcript while the keyword spotter was still confirming
// it. The KWS and ASR run in parallel: by the time KWS fires, the ASR stream
// can already contain "Mai, send a message to ...". Keeping that stream is what
// preserves the command prefix; stripping the leading wake word is what keeps
// the action parser able to see the command.
//
// This is deliberately conservative. "my" is only removed when the next word
// is a command trigger: "my name is ..." must not become "name is ...".
func stripWakeWordPrefix(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	prefixes := []struct {
		text           string
		requireCommand bool
	}{
		{"hey mai", false},
		{"hey my", false},
		{"mai", false},
		{"my", true},
	}
	for _, p := range prefixes {
		if !strings.HasPrefix(lower, p.text) {
			continue
		}
		if !wakeBoundary(trimmed, len(p.text)) {
			continue
		}
		rest := trimmed[len(p.text):]
		if strings.HasPrefix(strings.ToLower(rest), "'s") {
			rest = rest[2:]
		}
		rest = strings.TrimLeft(rest, " \t,.:;!?-")
		if p.requireCommand && !startsWithCommandTrigger(rest) {
			continue
		}
		return stripWakeWordPrefix(strings.TrimSpace(rest))
	}
	return trimmed
}

// wakeBoundary reports whether a wake-word match ends on a word boundary.
// Without it "maid" and "maybe" would be treated as the wake word.
func wakeBoundary(text string, end int) bool {
	if end >= len(text) {
		return true
	}
	c := text[end]
	return !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'))
}

func startsWithCommandTrigger(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	triggers := []string{
		"send", "message", "text", "email", "open", "launch", "start", "stop", "close", "quit",
		"play", "pause", "search", "look up", "google", "weather", "news",
		"type", "press", "click", "create", "write", "save", "delete",
		"rename", "move", "copy", "install", "run", "execute", "compile",
		"build", "set", "remind", "screenshot", "what", "who", "tell",
		"show", "turn", "volume", "brightness", "call", "read", "go", "do",
	}
	for _, t := range triggers {
		if lower == t || strings.HasPrefix(lower, t+" ") {
			return true
		}
	}
	return false
}

// dedupeRepeatedTranscript removes the two repetition patterns a streaming
// transducer produces when a turn is held open too long:
//
//   - adjacent duplicate n-grams ("send a message send a message to Manu")
//   - a repeated preamble ("On WhatsApp saying hello ... on WhatsApp saying hello")
//
// It is not a general grammar fixer. It only removes exact, normalized
// repetitions so the action parser has a chance to see one clean command.
func dedupeRepeatedTranscript(text string) string {
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) < 4 {
		return strings.TrimSpace(text)
	}

	// Adjacent duplicate runs, longest first.
	for n := 6; n >= 2; n-- {
		for i := 0; i+2*n <= len(words); {
			if equalWords(words[i:i+n], words[i+n:i+2*n]) {
				words = append(words[:i+n], words[i+2*n:]...)
				continue
			}
			i++
		}
	}

	// A repeated prefix is the other common form: the decoder restarts the
	// utterance without ever emitting an endpoint between the two attempts.
	// Require at least four words so ordinary repeated words ("no no") are
	// left alone.
	if len(words) >= 8 {
		for n := minInt(8, len(words)/2); n >= 4; n-- {
			// Removing a prefix shrinks words, so a later (smaller) n can
			// still be larger than the remaining half-length.
			if len(words) < 2*n {
				continue
			}
			if equalWords(words[:n], words[n:2*n]) {
				words = append(words[:0], words[n:]...)
				break
			}
			for j := n + 1; j+n <= len(words); j++ {
				if equalWords(words[:n], words[j:j+n]) {
					words = append(words[:0], words[n:]...)
					break
				}
			}
		}
	}

	// Collapse immediate single-word repeats left by a decoder loop.
	compact := words[:0]
	for _, w := range words {
		if len(compact) > 0 && normWord(compact[len(compact)-1]) == normWord(w) {
			continue
		}
		compact = append(compact, w)
	}
	return strings.TrimSpace(strings.Join(compact, " "))
}

// isLikelyRepetitiveTranscript reports whether a transcript still contains a
// repeated 3-word sequence after cleanup. That is the signal for the
// "I didn't catch that" path instead of sending a decoder loop to the LLM.
func isLikelyRepetitiveTranscript(text string) bool {
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) < 6 {
		return false
	}
	seen := make(map[string]bool, len(words))
	for i := 0; i+3 <= len(words); i++ {
		key := normWord(words[i]) + " " + normWord(words[i+1]) + " " + normWord(words[i+2])
		if seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}

func equalWords(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if normWord(a[i]) != normWord(b[i]) {
			return false
		}
	}
	return true
}

func normWord(w string) string {
	return strings.ToLower(strings.Trim(w, ".,!?;:\"'()[]{}"))
}
