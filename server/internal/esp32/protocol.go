package esp32

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
