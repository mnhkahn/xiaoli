package admin

import (
	"context"
	"net/http"
	"strings"

	agentesp32 "xiaoli/server/internal/esp32"
)

type DeviceController interface {
	Devices(ctx context.Context) ([]Device, error)
	Tools(ctx context.Context, deviceID string) (ToolListResponse, error)
	Call(ctx context.Context, request BridgeCallRequest) (BridgeCallResult, error)
	Speak(ctx context.Context, deviceID string, text string) (map[string]any, error)
	StopSpeak(ctx context.Context, deviceID string) (map[string]any, error)
}

type DeviceHub struct {
	*agentesp32.Hub
	conversation *deviceConversationAdapter
	vision       VisionAnalyzer
	tts          SpeechSynthesizer
}

func NewDeviceHub(cfg Config, stream *streamHub, audio *audioStore, asr SpeechRecognizer, agent *EinoAgent, vision VisionAnalyzer, tts SpeechSynthesizer) *DeviceHub {
	conversation := &deviceConversationAdapter{}
	hub := agentesp32.NewHub(agentesp32.HubConfig{
		PublicBaseURL:     cfg.PublicBaseURL,
		DeviceAuthKey:     cfg.DeviceAuthKey,
		AllowedDeviceIDs:  cfg.AllowedDeviceIDs,
		DeviceAuthEnabled: cfg.DeviceAuthEnabled,
	}, agentesp32.Dependencies{
		Stream:                    streamPublisher{stream: stream},
		ASR:                       asr,
		TTS:                       tts,
		Conversation:              conversation,
		NewVoiceDetector:          newVoiceDetector,
		BuildOggOpus:              buildOggOpus,
		ExtractOpusPackets:        extractOpusPackets,
		ReencodeOpusFrames:        reencodeOpusFrames,
		NormalizeImageContentType: normalizeImageContentType,
	})
	return &DeviceHub{Hub: hub, conversation: conversation, vision: vision, tts: tts}
}

func (h *DeviceHub) setConversation(pipeline *ConversationPipeline) {
	if h != nil && h.conversation != nil {
		h.conversation.pipeline = pipeline
	}
}

func (h *DeviceHub) deviceAllowed(deviceID string) bool {
	return h != nil && h.DeviceAllowed(deviceID)
}

func (h *DeviceHub) deviceAuthorized(r *http.Request) bool {
	return h != nil && h.DeviceAuthorized(r)
}

func (h *DeviceHub) Speak(ctx context.Context, deviceID string, text string) (map[string]any, error) {
	if h != nil && h.Hub != nil {
		h.Hub.SetTTS(h.tts)
	}
	return h.Hub.Speak(ctx, deviceID, text)
}

type streamPublisher struct {
	stream *streamHub
}

func (p streamPublisher) Publish(event agentesp32.StreamEvent) {
	if p.stream == nil {
		return
	}
	p.stream.publish(StreamEvent{
		Type:        event.Type,
		DeviceID:    event.DeviceID,
		ContentType: event.ContentType,
		Image:       event.Image,
		Size:        event.Size,
		TS:          event.TS,
		StreamID:    event.StreamID,
		Seq:         event.Seq,
		TimestampMS: event.TimestampMS,
	})
}

type deviceConversationAdapter struct {
	pipeline *ConversationPipeline
}

func (a *deviceConversationAdapter) AnswerDeviceText(ctx context.Context, deviceID string, text string) (string, error) {
	if a == nil || a.pipeline == nil {
		return "我现在还没有配置语言模型。", nil
	}
	reply, err := a.pipeline.Run(ctx, DeviceVoiceFactory{}.Build(deviceID, text))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(reply.Text), nil
}

func newVoiceDetector() (agentesp32.VoiceDetector, error) {
	return NewSileroVAD()
}
