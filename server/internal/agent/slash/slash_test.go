package slash

import (
	"context"
	"strings"
	"testing"

	"github.com/mnhkahn/xiaoli-esp32/server/internal/agent/channel"
	"github.com/mnhkahn/xiaoli-esp32/server/internal/agent/model"
)

type fakeDeps struct {
	current string
	used    string
}

func (fakeDeps) ListSkills(context.Context) ([]SkillInfo, error) {
	return []SkillInfo{{Name: "holiday", Description: "假期查询"}}, nil
}

func (d *fakeDeps) ModelInfo() ModelInfo {
	current := d.current
	if current == "" {
		current = "llm-test"
	}
	return ModelInfo{LLM: current, VLLM: "vllm-test"}
}

func (d *fakeDeps) ListModels(role model.Role) []ModelOption {
	if role != model.RoleLLM {
		return nil
	}
	return []ModelOption{{ID: "llm-test", Role: model.RoleLLM}, {ID: "llm-next", Role: model.RoleLLM}}
}

func (d *fakeDeps) UseModel(role model.Role, id string) error {
	d.used = string(role) + ":" + id
	d.current = id
	return nil
}

func (fakeDeps) ListChannels(context.Context) ([]channel.Info, error) {
	return []channel.Info{{ID: "lark:app:test", Type: channel.TypeLark}}, nil
}

func (fakeDeps) LLMStats(_ context.Context) string {
	return "test stats"
}

func (fakeDeps) NewSession(_ context.Context) string {
	return "test session"
}

func (fakeDeps) ListSessions(_ context.Context) string {
	return "test sessions"
}

func (fakeDeps) ResumeSession(_ context.Context, id string) string {
	return "resumed " + id
}

func (fakeDeps) SessionContext(_ context.Context, id string) string {
	return "test session context " + id
}

func (fakeDeps) ProviderBalances(_ context.Context) map[string]string {
	return map[string]string{"test-provider": "¥10.00", "free-provider": "N/A"}
}

func (fakeDeps) CompressSession(_ context.Context) string {
	return "压缩完成"
}

func (fakeDeps) MemoryList(_ context.Context) string {
	return "test memories"
}

func (fakeDeps) MemorySave(_ context.Context, _, _ string) string {
	return "保存成功"
}

func (fakeDeps) MemoryForget(_ context.Context, _ string) string {
	return "删除成功"
}

func (fakeDeps) MemoryClear(_ context.Context) string {
	return "清空成功"
}

func (fakeDeps) WorkflowList(_ context.Context) string {
	return "test workflows"
}

func (fakeDeps) WorkflowRun(_ context.Context, id string) string {
	return "已执行：" + id
}

func (fakeDeps) ReminderList(_ context.Context) string {
	return "test reminders"
}

func (fakeDeps) ReminderAdd(_ context.Context, at, text string) string {
	return "已创建提醒：" + text
}

func (fakeDeps) ReminderDelete(_ context.Context, id string) string {
	return "已删除提醒：" + id
}

func (fakeDeps) MCPStatus(_ context.Context) string {
	return "test mcp status"
}

func (fakeDeps) TaskStatusList(_ context.Context) string {
	return "test tasks"
}

func (fakeDeps) TaskStatusByID(_ context.Context, _ string) string {
	return "test task detail"
}

func (fakeDeps) TaskStatusListGrouped(_ context.Context) string {
	return "test tasks grouped"
}

func (fakeDeps) LogSearch(_ context.Context, _ string, _ int) string {
	return "test log search"
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
	handler := NewHandler(&fakeDeps{})

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

func TestResumeSession(t *testing.T) {
	handler := NewHandler(&fakeDeps{})

	reply, handled := handler.Handle(context.Background(), channel.TypeLark, "/resume ses_123")
	if !handled || !strings.Contains(reply, "resumed ses_123") {
		t.Fatalf("/resume reply=%q handled=%v, want resumed session", reply, handled)
	}

	reply, handled = handler.Handle(context.Background(), channel.TypeLark, "/resume")
	if !handled || !strings.Contains(reply, "用法") {
		t.Fatalf("/resume without id reply=%q handled=%v, want usage", reply, handled)
	}
}

func TestLogsAlias(t *testing.T) {
	handler := NewHandler(&fakeDeps{})

	reply, handled := handler.Handle(context.Background(), channel.TypeLark, "/logs error")
	if !handled || !strings.Contains(reply, "test log search") {
		t.Fatalf("/logs reply=%q handled=%v, want log search", reply, handled)
	}
}

func TestUnknownCommandIsNotHandled(t *testing.T) {
	handler := NewHandler(&fakeDeps{})

	reply, handled := handler.Handle(context.Background(), channel.TypeLark, "/unknown")

	if handled || reply != "" {
		t.Fatalf("unknown command handled=%v reply=%q, want passthrough", handled, reply)
	}
}

func TestSkillPromptForEnabledSkillSlash(t *testing.T) {
	handler := NewHandler(&fakeDeps{})

	prompt, ok := handler.SkillPrompt(context.Background(), "/holiday 2026-10-01")

	if !ok {
		t.Fatal("SkillPrompt() ok = false, want true")
	}
	for _, want := range []string{"holiday", "2026-10-01", "skill"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("SkillPrompt() = %q, want %q", prompt, want)
		}
	}
}

func TestSkillPromptSkipsBuiltinCommandConflict(t *testing.T) {
	handler := NewHandler(&fakeDeps{})

	prompt, ok := handler.SkillPrompt(context.Background(), "/model holiday")

	if ok || prompt != "" {
		t.Fatalf("SkillPrompt() = %q, %v; want skipped builtin conflict", prompt, ok)
	}
}

func TestSuggestionsIncludeBuiltinsAndSkills(t *testing.T) {
	handler := NewHandler(&fakeDeps{})

	got := handler.Suggestions(context.Background(), "/h")
	var names []string
	for _, item := range got {
		names = append(names, item.Kind+":"+item.Name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "cmd:help") || !strings.Contains(joined, "skill:holiday") {
		t.Fatalf("Suggestions(/h) = %#v, want help and holiday", got)
	}

	got = handler.Suggestions(context.Background(), "/mo")
	if len(got) != 1 || got[0].Name != "model" || got[0].Kind != "cmd" {
		t.Fatalf("Suggestions(/mo) = %#v, want model builtin only", got)
	}
}

func TestModelListAndUse(t *testing.T) {
	deps := &fakeDeps{current: "llm-test"}
	handler := NewHandler(deps)

	reply, handled := handler.Handle(context.Background(), channel.TypeLark, "/model list")
	if !handled || !strings.Contains(reply, "llm-test 当前") || !strings.Contains(reply, "llm-next") {
		t.Fatalf("/model list reply=%q handled=%v, want model options", reply, handled)
	}

	reply, handled = handler.Handle(context.Background(), channel.TypeLark, "/model use llm-next")
	if !handled || !strings.Contains(reply, "已切换 LLM 模型：llm-next") {
		t.Fatalf("/model use reply=%q handled=%v, want success", reply, handled)
	}
	if deps.used != "llm:llm-next" {
		t.Fatalf("used = %q, want llm:llm-next", deps.used)
	}

	reply, handled = handler.Handle(context.Background(), channel.TypeLark, "/model use asr asr-next")
	if !handled || !strings.Contains(reply, "只支持切换 LLM") {
		t.Fatalf("/model use asr reply=%q handled=%v, want unsupported role", reply, handled)
	}
}
