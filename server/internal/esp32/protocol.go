package esp32

import "github.com/mnhkahn/xiaoli/server/internal/notification"

const (
	AudioSampleRate      = 16000
	AudioChannels        = 1
	AudioFrameDurationMS = 60
)

func BuildHelloResponse(sessionID string) map[string]any {
	return map[string]any{
		"type":       "hello",
		"transport":  "websocket",
		"version":    1,
		"session_id": sessionID,
		"audio_params": map[string]any{
			"format":         "opus",
			"sample_rate":    AudioSampleRate,
			"channels":       AudioChannels,
			"frame_duration": AudioFrameDurationMS,
		},
	}
}

func BuildNotificationMessage(sessionID string, item notification.Notification) map[string]any {
	payload := map[string]any{
		"id":           item.ID,
		"title":        item.Title,
		"content":      item.Content,
		"published_at": item.PublishedAt,
	}
	if item.ExpiresAt != nil {
		payload["expires_at"] = item.ExpiresAt
	}
	return map[string]any{
		"type":         "notification",
		"version":      1,
		"session_id":   sessionID,
		"notification": payload,
	}
}
