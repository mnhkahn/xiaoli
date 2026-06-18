package lark

import (
	"encoding/json"
	"strings"
)

const (
	EventTypeURLVerification = "url_verification"
	EventTypeMessageReceive  = "im.message.receive_v1"
)

type Callback struct {
	Schema string `json:"schema"`
	Header struct {
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
		AppID     string `json:"app_id"`
		TenantKey string `json:"tenant_key"`
	} `json:"header"`
	Event json.RawMessage `json:"event"`

	Type      string `json:"type"`
	Challenge string `json:"challenge"`
}

func (c Callback) EventType() string {
	if c.Header.EventType != "" {
		return c.Header.EventType
	}
	return c.Type
}

func (c Callback) URLVerificationChallenge() string {
	if c.Challenge != "" {
		return c.Challenge
	}
	if len(c.Event) == 0 {
		return ""
	}
	var event ChallengeEvent
	_ = json.Unmarshal(c.Event, &event)
	return event.Challenge
}

type ChallengeEvent struct {
	Challenge string `json:"challenge"`
}

type MessageEvent struct {
	Sender struct {
		SenderType string `json:"sender_type"`
		SenderID   struct {
			OpenID  string `json:"open_id"`
			UserID  string `json:"user_id"`
			UnionID string `json:"union_id"`
		} `json:"sender_id"`
	} `json:"sender"`
	Message struct {
		MessageID   string `json:"message_id"`
		ChatID      string `json:"chat_id"`
		MessageType string `json:"message_type"`
		Content     string `json:"content"`
	} `json:"message"`
}

func (e MessageEvent) SenderID() string {
	if e.Sender.SenderID.OpenID != "" {
		return e.Sender.SenderID.OpenID
	}
	if e.Sender.SenderID.UserID != "" {
		return e.Sender.SenderID.UserID
	}
	return e.Sender.SenderID.UnionID
}

func (e MessageEvent) Text() string {
	if e.Message.MessageType != "text" {
		return ""
	}
	return ExtractText(e.Message.Content)
}

type TextContent struct {
	Text string `json:"text"`
}

func ExtractText(content string) string {
	var payload TextContent
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Text)
}
