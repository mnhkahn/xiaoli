package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestVoiceConversationKeepsOnlyTwoTurnsPerDevice(t *testing.T) {
	v := &voiceConversation{history: make(map[string][]*schema.Message)}
	for _, question := range []string{"第一问", "第二问", "第三问"} {
		v.remember("c3", question, question+"的回答")
	}
	messages := v.messages("c3", "第四问")
	if len(messages) != 6 {
		t.Fatalf("messages = %d, want system + two turns + current", len(messages))
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "第一问") {
			t.Fatalf("old voice turn leaked into prompt: %q", message.Content)
		}
	}
	if len(v.messages("s3", "独立设备")) != 2 {
		t.Fatal("voice history leaked between devices")
	}
}

func TestVoiceSentenceEmitsEarlyNaturalChunks(t *testing.T) {
	cases := []struct {
		input      string
		final      bool
		want, rest string
	}{
		{"你好。后一句", false, "你好。", "后一句"},
		{"这是一个确实比较长的前半句话，后面继续", false, "这是一个确实比较长的前半句话，", "后面继续"},
		{"短句", false, "", "短句"},
		{"短句", true, "短句", ""},
	}
	for _, tc := range cases {
		got, rest := voiceSentence(tc.input, tc.final)
		if got != tc.want || rest != tc.rest {
			t.Errorf("voiceSentence(%q) = (%q,%q), want (%q,%q)", tc.input, got, rest, tc.want, tc.rest)
		}
	}
}

func TestVoiceAdapterDoesNotUseFullAgentWhenVoiceConfigured(t *testing.T) {
	called := false
	a := &deviceConversationAdapter{
		voice: &voiceConversation{history: make(map[string][]*schema.Message)},
		pipeline: &ConversationPipeline{chat: conversationChatFunc(func(context.Context, ConversationTurn) (string, error) {
			called = true
			return "full agent", nil
		})},
	}
	_, _ = a.AnswerDeviceText(context.Background(), "c3", "你好")
	if called {
		t.Fatal("device voice entered the full Agent")
	}
}

func TestVoiceToolWhitelist(t *testing.T) {
	v := &voiceConversation{devices: voiceDeviceStub{tools: ToolListResponse{Ready: true, Tools: []map[string]any{
		{"name": "self.get_device_status", "description": "status"},
		{"name": "self.audio_speaker.set_volume", "description": "volume"},
		{"name": "self.camera.take_photo", "description": "photo"},
		{"name": "external.web_search", "description": "search"},
	}}}}
	tools := v.availableTools(context.Background(), "c3")
	if len(tools) != 3 {
		t.Fatalf("offered %d tools, want only three device tools", len(tools))
	}
	if !voiceToolOffered(tools, "set_volume") || voiceToolOffered(tools, "external.web_search") {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

type voiceDeviceStub struct{ tools ToolListResponse }

func (d voiceDeviceStub) Tools(context.Context, string) (ToolListResponse, error) {
	return d.tools, nil
}
func (d voiceDeviceStub) Devices(context.Context) ([]Device, error) { return nil, nil }
func (d voiceDeviceStub) Call(context.Context, BridgeCallRequest) (BridgeCallResult, error) {
	return BridgeCallResult{}, nil
}
func (d voiceDeviceStub) Speak(context.Context, string, string) (map[string]any, error) {
	return nil, nil
}
func (d voiceDeviceStub) StopSpeak(context.Context, string) (map[string]any, error) { return nil, nil }
