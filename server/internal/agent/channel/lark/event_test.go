package lark

import (
	"encoding/json"
	"testing"
)

func TestCallbackEventTypeFallsBackToLegacyType(t *testing.T) {
	callback := Callback{Type: EventTypeURLVerification}
	if got := callback.EventType(); got != EventTypeURLVerification {
		t.Fatalf("EventType() = %q, want %q", got, EventTypeURLVerification)
	}

	callback.Header.EventType = EventTypeMessageReceive
	if got := callback.EventType(); got != EventTypeMessageReceive {
		t.Fatalf("EventType() = %q, want header event type", got)
	}
}

func TestCallbackURLVerificationChallenge(t *testing.T) {
	callback := Callback{Challenge: "top-level"}
	if got := callback.URLVerificationChallenge(); got != "top-level" {
		t.Fatalf("URLVerificationChallenge() = %q, want top-level", got)
	}

	callback = Callback{Event: json.RawMessage(`{"challenge":"event-level"}`)}
	if got := callback.URLVerificationChallenge(); got != "event-level" {
		t.Fatalf("URLVerificationChallenge() = %q, want event-level", got)
	}
}

func TestMessageEventSenderIDAndText(t *testing.T) {
	var event MessageEvent
	event.Sender.SenderID.UnionID = "union"
	event.Sender.SenderID.UserID = "user"
	event.Sender.SenderID.OpenID = "open"
	event.Message.MessageType = "text"
	event.Message.Content = `{"text":"  你好  "}`

	if got := event.SenderID(); got != "open" {
		t.Fatalf("SenderID() = %q, want open", got)
	}
	if got := event.Text(); got != "你好" {
		t.Fatalf("Text() = %q, want trimmed text", got)
	}

	event.Message.MessageType = "image"
	if got := event.Text(); got != "" {
		t.Fatalf("Text() = %q, want empty for non-text", got)
	}
}
