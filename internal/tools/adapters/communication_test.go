package adapters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWhatsAppToolRequiresRecipientAndMessage(t *testing.T) {
	called := false
	tool := &WhatsAppTool{Send: func(app, contact, text string) error {
		called = true
		return nil
	}}

	tests := []struct {
		name   string
		params string
		want   string
	}{
		{"missing recipient", `{"message":"hello"}`, "recipient is required"},
		{"blank recipient", `{"message":"hello","recipient":"   "}`, "recipient is required"},
		{"missing message", `{"recipient":"Priya"}`, "message is required"},
		{"blank message", `{"message":"  ","recipient":"Priya"}`, "message is required"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			result, err := tool.Execute(context.Background(), json.RawMessage(tc.params))
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if result.Error == nil || !strings.Contains(result.Error.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, result.Error)
			}
			if called {
				t.Fatal("Send must not be called for an invalid request")
			}
		})
	}
}

func TestWhatsAppToolPreparesDraft(t *testing.T) {
	var gotApp, gotContact, gotText string
	tool := &WhatsAppTool{Send: func(app, contact, text string) error {
		gotApp, gotContact, gotText = app, contact, text
		return nil
	}}

	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"message":"Hello World","recipient":"Priya"}`))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected tool error: %v", result.Error)
	}
	if gotApp != "whatsapp" || gotContact != "Priya" || gotText != "Hello World" {
		t.Fatalf("unexpected send arguments: app=%q contact=%q text=%q", gotApp, gotContact, gotText)
	}
	if !strings.Contains(result.Output, "Prepared") || !strings.Contains(result.Output, "confirm") {
		t.Fatalf("output should describe a prepared draft: %q", result.Output)
	}
}

func TestWhatsAppToolSchemaRequiresBothFields(t *testing.T) {
	tool := &WhatsAppTool{}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(tool.Metadata().Parameters, &schema); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
	seen := map[string]bool{}
	for _, field := range schema.Required {
		seen[field] = true
	}
	if !seen["message"] || !seen["recipient"] {
		t.Fatalf("schema must require message and recipient, got %v", schema.Required)
	}
}
