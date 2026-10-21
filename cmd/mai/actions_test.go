package main

import "testing"

func TestActionParserMessageForms(t *testing.T) {
	parser := NewActionParser()

	tests := []struct {
		name    string
		input   string
		contact string
		app     string
		text    string
	}{
		{
			name:    "placeholder body asks for text",
			input:   "send a message to Priya in whatsapp",
			contact: "Priya",
			app:     "whatsapp",
			text:    "",
		},
		{
			name:    "inline body keeps case",
			input:   "send Hello World to mom in whatsapp",
			contact: "mom",
			app:     "whatsapp",
			text:    "Hello World",
		},
		{
			name:    "saying body keeps case",
			input:   "send a message to John on whatsapp saying Hello There",
			contact: "John",
			app:     "whatsapp",
			text:    "Hello There",
		},
		{
			name:    "tell form",
			input:   "tell Ravi on whatsapp Hello There",
			contact: "Ravi",
			app:     "whatsapp",
			text:    "Hello There",
		},
		{
			name:    "message form",
			input:   "message Priya in whatsapp Hello There",
			contact: "Priya",
			app:     "whatsapp",
			text:    "Hello There",
		},
		{
			name:    "find and tell form",
			input:   "find John on whatsapp and tell him Hello There",
			contact: "John",
			app:     "whatsapp",
			text:    "Hello There",
		},
		{
			name:    "via form",
			input:   "send hello to mom via whatsapp",
			contact: "mom",
			app:     "whatsapp",
			text:    "hello",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := parser.ParseFromAnywhere(tc.input)
			if action.Type != ActionSendMsg {
				t.Fatalf("expected ActionSendMsg, got %s", action.Type)
			}
			if got := action.Params["contact"]; got != tc.contact {
				t.Fatalf("contact = %v, want %q", got, tc.contact)
			}
			if got := action.Params["app"]; got != tc.app {
				t.Fatalf("app = %v, want %q", got, tc.app)
			}
			if got, _ := action.Params["text"].(string); got != tc.text {
				t.Fatalf("text = %q, want %q", got, tc.text)
			}
		})
	}
}

// TestActionParserCapturesRecipientFromInput is the regression guard against a
// hardcoded/default recipient. Every case uses a different name or number and
// asserts that the parser returns exactly that value.
func TestActionParserCapturesRecipientFromInput(t *testing.T) {
	parser := NewActionParser()

	tests := []struct {
		input   string
		contact string
		text    string
	}{
		{
			input:   "send hello to mom in whatsapp",
			contact: "mom",
			text:    "hello",
		},
		{
			input:   "send a message to Priya on whatsapp saying see you tomorrow",
			contact: "Priya",
			text:    "see you tomorrow",
		},
		{
			input:   "tell John on whatsapp I am running late",
			contact: "John",
			text:    "I am running late",
		},
		{
			input:   "send hello to +1 555 123 4567 on whatsapp",
			contact: "+1 555 123 4567",
			text:    "hello",
		},
		{
			input:   "send running late to 9876543210 via whatsapp",
			contact: "9876543210",
			text:    "running late",
		},
	}

	for _, tc := range tests {
		t.Run(tc.contact, func(t *testing.T) {
			action := parser.ParseFromAnywhere(tc.input)
			if action.Type != ActionSendMsg {
				t.Fatalf("%q: expected ActionSendMsg, got %s", tc.input, action.Type)
			}
			if got, _ := action.Params["contact"].(string); got != tc.contact {
				t.Fatalf("%q: contact = %q, want %q", tc.input, got, tc.contact)
			}
			if got, _ := action.Params["text"].(string); got != tc.text {
				t.Fatalf("%q: text = %q, want %q", tc.input, got, tc.text)
			}
		})
	}
}

func TestActionParserDoesNotInventRecipient(t *testing.T) {
	parser := NewActionParser()
	for _, input := range []string{
		"send hello to whatsapp",
		"message whatsapp hello",
	} {
		action := parser.ParseFromAnywhere(input)
		if action.Type == ActionSendMsg {
			contact, _ := action.Params["contact"].(string)
			if contact == "" {
				t.Fatalf("%q parsed as a send with no recipient: %+v", input, action)
			}
		}
	}
}

// TestActionParserNoStartupSideEffects documents and enforces that constructing
// the parser/executor, or processing empty input, cannot start automation. The
// executor is given a nil Automation on purpose: if it tried to prepare or send
// anything this test would panic instead of touching WhatsApp.
func TestActionParserNoStartupSideEffects(t *testing.T) {
	parser := NewActionParser()
	for _, input := range []string{"", "   ", "\n"} {
		if got := parser.Parse(input); got.Type != ActionNone {
			t.Fatalf("Parse(%q) = %s, want no action", input, got.Type)
		}
		if got := parser.ParseFromAnywhere(input); got.Type != ActionNone {
			t.Fatalf("ParseFromAnywhere(%q) = %s, want no action", input, got.Type)
		}
	}

	executor := NewActionExecutor(nil)
	if executor.pending != nil {
		t.Fatal("a new executor must not have a pending message")
	}
	executed, feedback, err := executor.ParseAndExecute("")
	if err != nil {
		t.Fatalf("empty input returned an error: %v", err)
	}
	if executed || feedback != "" {
		t.Fatalf("empty input must not execute an action (executed=%v feedback=%q)", executed, feedback)
	}
}

func TestPlaceholderMessages(t *testing.T) {
	for _, text := range []string{"", "  ", "a message", "message", "text", "msg", "something", "Message."} {
		if !isPlaceholderMessage(text) {
			t.Fatalf("expected %q to be a placeholder", text)
		}
	}
	for _, text := range []string{"hello", "Hello World", "something happened", "a message about lunch"} {
		if isPlaceholderMessage(text) {
			t.Fatalf("expected %q to be a real message", text)
		}
	}
}

func TestConfirmationPhrases(t *testing.T) {
	for _, text := range []string{"yes", "Yes, please", "ok", "okay", "send", "send it", "go ahead", "confirm"} {
		if !isAffirmative(text) {
			t.Fatalf("expected %q to be affirmative", text)
		}
	}
	for _, text := range []string{"no", "nope", "cancel", "stop", "don't send", "do not send", "never mind"} {
		if !isNegative(text) {
			t.Fatalf("expected %q to be negative", text)
		}
	}
	if isAffirmative("send a message to Priya") {
		t.Fatal("a new command must not confirm a pending send")
	}
}
