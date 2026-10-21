package main

import (
	"errors"
	"testing"
)

func TestPrepareMessageRequiresUserTurn(t *testing.T) {
	auto := &Automation{}

	if err := auto.PrepareMessage("whatsapp", "Priya", "hello"); !errors.Is(err, ErrMessageRequiresUserTurn) {
		t.Fatalf("expected ErrMessageRequiresUserTurn outside a user turn, got %v", err)
	}

	auto.BeginUserTurn()
	if !auto.InUserTurn() {
		t.Fatal("expected InUserTurn to be true after BeginUserTurn")
	}
	auto.EndUserTurn()
	if auto.InUserTurn() {
		t.Fatal("expected InUserTurn to be false after EndUserTurn")
	}
}

func TestPrepareMessageAllowsUserTurnToReachValidation(t *testing.T) {
	auto := &Automation{}
	auto.BeginUserTurn()
	defer auto.EndUserTurn()

	// Use a non-WhatsApp app so validation returns before any desktop
	// automation runs. The point is that the user-turn guard no longer blocks.
	err := auto.PrepareMessage("telegram", "Priya", "hello")
	if errors.Is(err, ErrMessageRequiresUserTurn) {
		t.Fatal("guard still active during an explicit user turn")
	}
	if err == nil {
		t.Fatal("expected a validation error for an unsupported app")
	}
}

func TestConfirmSendRequiresUserTurn(t *testing.T) {
	auto := &Automation{
		pending: &pendingMessage{App: "whatsapp", Contact: "Priya", Text: "hello"},
	}

	if err := auto.ConfirmSend(); !errors.Is(err, ErrMessageRequiresUserTurn) {
		t.Fatalf("expected ErrMessageRequiresUserTurn outside a user turn, got %v", err)
	}
}
