package slash

import (
	"context"
	"strings"
	"testing"

	"xiaoli/server/internal/agent/channel"
)

type fakeDeps struct{}

func (fakeDeps) ListSkills(context.Context) ([]SkillInfo, error) {
	return []SkillInfo{{Name: "holiday", Description: "假期查询"}}, nil
}

func (fakeDeps) ModelInfo() ModelInfo {
	return ModelInfo{LLM: "llm-test", VLLM: "vllm-test"}
}

func (fakeDeps) ListChannels(context.Context) ([]channel.Info, error) {
	return []channel.Info{{ID: "lark:app:test", Type: channel.TypeLark}}, nil
}

func TestParseRequiresLeadingSlash(t *testing.T) {
	cmd, ok := Parse(" /skills  --verbose ")
	if !ok {
		t.Fatal("Parse() ok = false, want true")
	}
	if cmd.Name != "skills" || cmd.Args != "--verbose" {
		t.Fatalf("command = %#v, want skills with args", cmd)
	}
	if _, ok := Parse("hello /skills"); ok {
		t.Fatal("Parse() accepted non-leading slash command")
	}
}

func TestHandleBuiltinsAndRejectsESP32(t *testing.T) {
	handler := NewHandler(fakeDeps{})

	reply, handled := handler.Handle(context.Background(), channel.TypeLark, "/model")
	if !handled || !strings.Contains(reply, "llm-test") || !strings.Contains(reply, "vllm-test") {
		t.Fatalf("/model reply=%q handled=%v, want model info", reply, handled)
	}

	reply, handled = handler.Handle(context.Background(), channel.TypeWechat, "/skills")
	if !handled || !strings.Contains(reply, "holiday") {
		t.Fatalf("/skills reply=%q handled=%v, want skill list", reply, handled)
	}

	reply, handled = handler.Handle(context.Background(), channel.TypeESP32, "/model")
	if handled || reply != "" {
		t.Fatalf("ESP32 handled=%v reply=%q, want passthrough", handled, reply)
	}
}

func TestUnknownCommandIsNotHandled(t *testing.T) {
	handler := NewHandler(fakeDeps{})

	reply, handled := handler.Handle(context.Background(), channel.TypeLark, "/unknown")

	if handled || reply != "" {
		t.Fatalf("unknown command handled=%v reply=%q, want passthrough", handled, reply)
	}
}
