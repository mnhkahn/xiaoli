package admin

import (
	"encoding/json"
	"strings"
	"time"

	agentesp32 "xiaoli/server/internal/esp32"
)

func needsVision(text string) bool {
	text = strings.ToLower(text)
	for _, marker := range []string{"看", "看看", "照片", "图片", "图像", "摄像头", "画面", "拍", "坐姿", "学习状态", "我现在"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func extractMCPText(value any) string {
	switch v := value.(type) {
	case string:
		var parsed any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			if text := extractMCPText(parsed); text != "" {
				return text
			}
		}
		return v
	case map[string]any:
		if content, ok := v["content"].([]any); ok {
			var parts []string
			for _, item := range content {
				if m, ok := item.(map[string]any); ok {
					if text := strings.TrimSpace(stringValue(m["text"])); text != "" {
						parts = append(parts, extractMCPText(text))
					}
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
		for _, key := range []string{"response", "answer", "text", "message", "summary", "analysis", "result"} {
			if text := strings.TrimSpace(stringValue(v[key])); text != "" && text != "<nil>" {
				return text
			}
		}
	case []any:
		var parts []string
		for _, item := range v {
			if text := strings.TrimSpace(extractMCPText(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func assistantAudioSendDeadline(pacedStart time.Time, packetIndex int, frameDuration time.Duration) time.Time {
	return agentesp32.AssistantAudioSendDeadline(pacedStart, packetIndex, frameDuration)
}
