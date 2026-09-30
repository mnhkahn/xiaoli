package esp32

import (
	"context"
	"testing"
	"time"
)

type voiceSelectorTTS struct {
	voices []string
}

func (s *voiceSelectorTTS) Synthesize(context.Context, string) (string, []byte, error) {
	s.voices = append(s.voices, "default")
	return "audio/ogg", []byte("ogg"), nil
}

func (s *voiceSelectorTTS) SynthesizeWithVoice(_ context.Context, _, voice string) (string, []byte, error) {
	s.voices = append(s.voices, voice)
	return "audio/ogg", []byte("ogg"), nil
}

func TestPrepareAssistantAudioSelectsVoiceByDevice(t *testing.T) {
	synth := &voiceSelectorTTS{}
	hub := NewHub(HubConfig{}, Dependencies{
		TTS: synth,
		TTSVoiceForDevice: func(id string) string {
			if id == "c3" {
				return "custom"
			}
			return ""
		},
		ExtractOpusPackets: func([]byte) ([][]byte, time.Duration) {
			return [][]byte{{1}}, 20 * time.Millisecond
		},
	})
	for _, id := range []string{"c3", "other"} {
		if _, err := hub.prepareAssistantAudio(context.Background(), &Session{deviceID: id}, "你好"); err != nil {
			t.Fatal(err)
		}
	}
	if len(synth.voices) != 2 || synth.voices[0] != "custom" || synth.voices[1] != "default" {
		t.Fatalf("voices = %q", synth.voices)
	}
}
