package esp32

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mnhkahn/xiaoli/server/internal/notification"
)

func TestBuildNotificationMessage(t *testing.T) {
	publishedAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	message := BuildNotificationMessage("session-1", notification.Notification{
		ID: "ntf_1", Title: "标题", Content: "正文", PublishedAt: publishedAt,
	})
	if message["type"] != "notification" || message["version"] != 1 || message["session_id"] != "session-1" {
		t.Fatalf("message = %#v", message)
	}
	payload := message["notification"].(map[string]any)
	if payload["id"] != "ntf_1" || payload["title"] != "标题" || payload["content"] != "正文" {
		t.Fatalf("payload = %#v", payload)
	}
	if _, exists := payload["expires_at"]; exists {
		t.Fatalf("empty expires_at should be omitted: %#v", payload)
	}
}

func TestNotificationsEnabled(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want bool
	}{
		{name: "enabled", body: `{"features":{"notifications":true,"mcp":true}}`, want: true},
		{name: "disabled", body: `{"features":{"notifications":false}}`, want: false},
		{name: "legacy", body: `{"features":{"mcp":true}}`, want: false},
		{name: "invalid", body: `{"features":"bad"}`, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var message map[string]json.RawMessage
			if err := json.Unmarshal([]byte(test.body), &message); err != nil {
				t.Fatal(err)
			}
			if got := notificationsEnabled(message); got != test.want {
				t.Fatalf("notificationsEnabled = %v, want %v", got, test.want)
			}
		})
	}
}
