package esp32

import "testing"

func TestBuildHelloResponseIncludesAudioParams(t *testing.T) {
	got := BuildHelloResponse("session-1")
	if got["type"] != "hello" || got["session_id"] != "session-1" {
		t.Fatalf("hello = %#v, want type and session", got)
	}
	audio, ok := got["audio_params"].(map[string]any)
	if !ok {
		t.Fatalf("audio_params = %#v, want map", got["audio_params"])
	}
	if audio["format"] != "opus" || audio["sample_rate"] != AudioSampleRate || audio["channels"] != AudioChannels || audio["frame_duration"] != AudioFrameDurationMS {
		t.Fatalf("audio_params = %#v, want opus %d/%d/%d", audio, AudioSampleRate, AudioChannels, AudioFrameDurationMS)
	}
}
