package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
	agentworkflow "github.com/mnhkahn/xiaoli/internal/agent/workflow"
	agentevent "github.com/mnhkahn/xiaoli/internal/event"
)

type testTool struct {
	name string
}

func TestAssistantResultUsesAskMessageInsteadOfGenericFallback(t *testing.T) {
	ask := &agentbuiltin.AskData{
		Question: "要部署到生产环境吗？",
		Options:  []string{"是", "否"},
	}

	msg := assistantResultAfterRun(nil, "", ask, nil)
	if msg == nil {
		t.Fatal("assistantResultAfterRun() returned nil")
	}
	if strings.Contains(msg.Content, "命令或工具已执行完成") {
		t.Fatalf("assistantResultAfterRun() content = %q, want ask-specific message", msg.Content)
	}
	if !strings.Contains(msg.Content, "要部署到生产环境吗") {
		t.Fatalf("assistantResultAfterRun() content = %q, want question prompt", msg.Content)
	}
}

func TestAssistantResultUsesPendingToolConfirmFallbackWhenNoText(t *testing.T) {
	confirm := &agentbuiltin.PendingToolUseConfirm{
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_abc123",
		Question:    "是否允许执行命令：git status --short",
		Options:     []string{"允许一次 :: git status --short", "拒绝"},
		BashHash:    "abc123",
		BashCommand: "git status --short",
	}

	msg := assistantResultAfterRun(nil, "", nil, confirm)
	if msg == nil {
		t.Fatal("assistantResultAfterRun() returned nil")
	}
	for _, want := range []string{"等待你确认执行 bash 命令", "git status --short", "toolu_bash_abc123", "审批面板"} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("assistantResultAfterRun() content = %q, want %q", msg.Content, want)
		}
	}
	if strings.TrimSpace(msg.Content) == "等待你确认命令。" {
		t.Fatalf("assistantResultAfterRun() returned lossy placeholder")
	}
}

func TestAssistantResultKeepsTextWhenToolUseConfirmIsPending(t *testing.T) {
	confirm := &agentbuiltin.PendingToolUseConfirm{
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_nrql",
		Question:    "是否允许执行命令：newrelic nrql query --accountId 6119564",
		Options:     []string{"允许一次 :: newrelic nrql query --accountId 6119564", "拒绝"},
		BashHash:    "abc123",
		BashCommand: "newrelic nrql query --accountId 6119564",
	}

	msg := assistantResultAfterRun(schema.AssistantMessage("字段名可能不对，先看 PageViewTiming 有哪些字段：", nil), "", nil, confirm)
	if msg == nil {
		t.Fatal("assistantResultAfterRun() returned nil")
	}
	if !strings.Contains(msg.Content, "字段名可能不对") {
		t.Fatalf("assistantResultAfterRun() content = %q, want assistant explanation preserved", msg.Content)
	}
	if strings.Contains(msg.Content, "是否允许执行命令") {
		t.Fatalf("assistantResultAfterRun() mixed permission request into assistant text: %q", msg.Content)
	}
}

func TestToolGuideRequiresRealBashToolUse(t *testing.T) {
	agent := &Agent{cfg: Config{BashConfig: BashConfig{Enabled: true}}}

	got := agent.toolGuide(true)

	for _, want := range []string{"必须调用 bash 工具", "禁止手写", "等待你确认执行 bash 命令", "任务结束后仍必须给出可核验的简短摘要", "完全静默"} {
		if !strings.Contains(got, want) {
			t.Fatalf("toolGuide() = %q, want %q", got, want)
		}
	}
}

func TestStoreToolUseConfirmPublishesPermissionAskedEvent(t *testing.T) {
	bus := agentevent.NewBus()
	events := make(chan agentevent.Event, 1)
	unsub := bus.Subscribe(agentevent.TypePermissionAsked, func(ctx context.Context, e agentevent.Event) error {
		events <- e
		return nil
	})
	defer unsub()
	agent := &Agent{eventBus: bus}
	confirm := &agentbuiltin.PendingToolUseConfirm{
		RequestID:   "toolu_bash_evt",
		SessionID:   "ses_evt",
		ChannelName: "lark_text",
		DeviceID:    "ou_user",
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_evt",
		Question:    "是否允许执行命令：git status",
		Options:     []string{"允许一次", "拒绝"},
		BashHash:    "hash",
		BashCommand: "git status",
	}

	agent.storeToolUseConfirm(context.Background(), "lark:chat:user", confirm)

	select {
	case e := <-events:
		if e.Type != agentevent.TypePermissionAsked || e.SessionID != "ses_evt" {
			t.Fatalf("event = %#v, want permission asked for session", e)
		}
		data, ok := e.Data.(agentevent.PermissionAskedData)
		if !ok {
			t.Fatalf("event data = %#v, want PermissionAskedData", e.Data)
		}
		if data.ToolName != "bash" || data.ToolUseID != "toolu_bash_evt" || data.Input["command"] != "git status" || data.ChannelName != "lark_text" || data.DeviceID != "ou_user" {
			t.Fatalf("permission data = %#v, want structured tool use details", data)
		}
	case <-time.After(time.Second):
		t.Fatal("permission.asked event was not published")
	}
}

func TestStoreToolUseConfirmCanBeConsumedBySessionID(t *testing.T) {
	agent := &Agent{}
	confirm := &agentbuiltin.PendingToolUseConfirm{
		RequestID:   "toolu_bash_session",
		SessionID:   "ses_tool_use",
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_session",
		Question:    "是否允许执行命令：git status",
		Options:     []string{"允许一次", "拒绝"},
		BashHash:    "hash",
		BashCommand: "git status",
	}

	agent.storeToolUseConfirm(context.Background(), "local", confirm)

	got := agent.ConsumeToolUseConfirm("ses_tool_use")
	if got == nil || got.ToolUseID != "toolu_bash_session" {
		t.Fatalf("ConsumeToolUseConfirm(session) = %#v, want stored confirm", got)
	}
	if again := agent.ConsumeToolUseConfirm("local"); again != nil {
		t.Fatalf("ConsumeToolUseConfirm(conversation) after session consume = %#v, want nil", again)
	}
}

func TestStoreToolUseConfirmQueuesMultipleConfirms(t *testing.T) {
	agent := &Agent{}
	first := &agentbuiltin.PendingToolUseConfirm{
		RequestID:   "toolu_bash_first",
		SessionID:   "ses_tool_use",
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_first",
		Question:    "是否允许执行命令：first",
		Options:     []string{"允许一次", "拒绝"},
		BashHash:    "hash_first",
		BashCommand: "first",
	}
	second := &agentbuiltin.PendingToolUseConfirm{
		RequestID:   "toolu_bash_second",
		SessionID:   "ses_tool_use",
		ToolName:    "bash",
		ToolUseID:   "toolu_bash_second",
		Question:    "是否允许执行命令：second",
		Options:     []string{"允许一次", "拒绝"},
		BashHash:    "hash_second",
		BashCommand: "second",
	}

	agent.storeToolUseConfirm(context.Background(), "local", first)
	agent.storeToolUseConfirm(context.Background(), "local", second)

	if got := agent.ConsumeToolUseConfirm("ses_tool_use"); got == nil || got.ToolUseID != "toolu_bash_first" {
		t.Fatalf("first ConsumeToolUseConfirm() = %#v, want first", got)
	}
	if got := agent.ConsumeToolUseConfirm("ses_tool_use"); got == nil || got.ToolUseID != "toolu_bash_second" {
		t.Fatalf("second ConsumeToolUseConfirm() = %#v, want second", got)
	}
	if got := agent.ConsumeToolUseConfirm("ses_tool_use"); got != nil {
		t.Fatalf("third ConsumeToolUseConfirm() = %#v, want nil", got)
	}
}

func (t testTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: t.name}, nil
}

func toolNames(t *testing.T, tools []tool.BaseTool) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, tb := range tools {
		info, err := tb.Info(context.Background())
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if info != nil {
			names[info.Name] = true
		}
	}
	return names
}

func TestToolsForChatIncludesConfiguredBuiltinTools(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	tools := agent.toolsForChat(context.Background(), "", "", "")
	if len(tools) != 3 {
		t.Fatalf("tools len = %d, want 3 (webfetch + websearch + ask, no taskTool set)", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Name != "webfetch" {
		t.Fatalf("tool[0] name = %q, want webfetch", info.Name)
	}
	info2, err := tools[1].Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info2.Name != "websearch" {
		t.Fatalf("tool[1] name = %q, want websearch", info2.Name)
	}
	info3, err := tools[2].Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info3.Name != "ask_user_question" {
		t.Fatalf("tool[2] name = %q, want ask_user_question", info3.Name)
	}
}

func TestToolsForChatIncludesTaskToolWhenSet(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	agent.taskTool = agentbuiltin.NewTaskTool(
		agentbuiltin.DefaultSubAgents(),
		func(ctx context.Context, spec agentbuiltin.SubAgentSpec, rt *agentbuiltin.SubAgentRuntime, prompt string) (string, error) {
			return "", nil
		},
		nil,
	)
	tools := agent.toolsForChat(context.Background(), "", "", "")
	found := false
	for _, tb := range tools {
		info, _ := tb.Info(context.Background())
		if info.Name == "task" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("toolsForChat should include task tool when taskTool is set")
	}
}

func TestToolsForChatIncludesChannelSendToolWhenConfigured(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	agent.SetChannelSenders(ChannelSendersConfig{AllowedRoots: []string{t.TempDir()}})

	tools := agent.toolsForChat(context.Background(), "", "", "")
	foundChannelSend := false
	foundFileWrite := false
	for _, tb := range tools {
		info, err := tb.Info(context.Background())
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if info.Name == "channel_send" {
			foundChannelSend = true
		}
		if info.Name == "file_write" {
			foundFileWrite = true
		}
	}
	if !foundChannelSend {
		t.Fatal("toolsForChat should include channel_send when channel senders are configured")
	}
	if !foundFileWrite {
		t.Fatal("toolsForChat should include file_write when channel senders are configured")
	}
}

func TestToolsForChatIncludesFileWriteWithoutChannelSend(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	agent.SetFileWriteRoots([]string{t.TempDir()})

	names := toolNames(t, agent.toolsForChat(context.Background(), "", "", "tui"))
	if !names["file_write"] {
		t.Fatalf("toolsForChat missing file_write: %#v", names)
	}
	if names["channel_send"] {
		t.Fatalf("toolsForChat should not include channel_send without configured sender: %#v", names)
	}
}

func TestToolsForChatIncludesCodeFileToolsWhenConfigured(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	agent.SetAgentFileRoots([]string{t.TempDir()})

	names := toolNames(t, agent.toolsForChat(context.Background(), "", "", "tui"))
	for _, want := range []string{"glob", "read_file", "grep", "edit_file"} {
		if !names[want] {
			t.Fatalf("toolsForChat missing %q: %#v", want, names)
		}
	}
}

func TestFilterDisabledToolsRemovesNamedTools(t *testing.T) {
	tools := []tool.BaseTool{testTool{name: "glob"}, testTool{name: "edit_file"}, testTool{name: "bash"}}
	filtered := filterDisabledTools(context.Background(), tools, []string{"edit_file", "bash"})

	names := toolNames(t, filtered)
	if !names["glob"] || names["edit_file"] || names["bash"] {
		t.Fatalf("filtered tools = %#v", names)
	}
}

func TestToolsForChatIncludesReminderToolsWhenConfigured(t *testing.T) {
	agent := &Agent{cfg: Config{
		BuiltinWebFetchEnabled: true,
		Timezone:               "Asia/Shanghai",
		ReminderStore:          agentworkflow.NewReminderStore(t.TempDir() + "/reminders.json"),
	}}

	names := toolNames(t, agent.toolsForChat(context.Background(), "", "", "tui"))
	for _, want := range []string{"reminder_add", "reminder_list", "reminder_delete"} {
		if !names[want] {
			t.Fatalf("toolsForChat missing %q: %#v", want, names)
		}
	}
}

func TestToolsForChatIncludesBashForTUIWhenEnabled(t *testing.T) {
	agent := &Agent{cfg: Config{BashConfig: BashConfig{
		Enabled:        true,
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	}}}
	names := toolNames(t, agent.toolsForChat(context.Background(), "ses_test", "local", "tui"))
	if !names["bash"] {
		t.Fatalf("toolsForChat for tui missing bash: %#v", names)
	}
}

func TestToolsForChatDoesNotIncludeBashForUnknownChannel(t *testing.T) {
	agent := &Agent{cfg: Config{BashConfig: BashConfig{
		Enabled:        true,
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	}}}
	names := toolNames(t, agent.toolsForChat(context.Background(), "ses_test", "local", ""))
	if names["bash"] {
		t.Fatalf("toolsForChat for empty channel includes bash: %#v", names)
	}
}

func TestSubAgentToolsNotIncludesInteractiveTools(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	tools := agent.subAgentTools(context.Background(), true, "")
	for _, tb := range tools {
		info, err := tb.Info(context.Background())
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if info.Name == "ask_user_question" || info.Name == "task" || info.Name == "memory_save" || info.Name == "memory_forget" || info.Name == "memory_list" || info.Name == "file_write" || info.Name == "channel_send" {
			t.Fatalf("subAgentTools should not include tool %q", info.Name)
		}
	}
}

func TestGenerateToolsNotIncludesInteractiveTools(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	tools := agent.generateTools(context.Background())
	for _, tb := range tools {
		info, err := tb.Info(context.Background())
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if info.Name == "ask_user_question" || info.Name == "task" || info.Name == "memory_save" || info.Name == "memory_forget" || info.Name == "memory_list" {
			t.Fatalf("generateTools should not include tool %q", info.Name)
		}
	}
}

func TestToolsForChatSkipsMemoryWithoutBackend(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	toolsNoMem := agent.toolsForChat(context.Background(), "", "", "")
	for _, tb := range toolsNoMem {
		info, err := tb.Info(context.Background())
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if info.Name == "memory_save" || info.Name == "memory_forget" || info.Name == "memory_list" {
			t.Fatalf("toolsForChat without memory backend should not include tool %q", info.Name)
		}
	}
}

func TestSubAgentToolsDenyToolsReturnsNil(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	tools := agent.subAgentTools(context.Background(), false, "")
	if len(tools) != 0 {
		t.Fatalf("subAgentTools(deny) should be empty, got %d", len(tools))
	}
}

func TestA2AToolsOnlyExposePublicAllowlist(t *testing.T) {
	agent := &Agent{
		cfg: Config{BuiltinWebFetchEnabled: true, LogDir: t.TempDir()},
		extMCPNames: []string{
			"CYEAM",
			"AMap",
			"github",
		},
		extToolSets: [][]tool.BaseTool{
			{testTool{name: "cyeam_public"}},
			{testTool{name: "amap_weather"}},
			{testTool{name: "github_repo"}},
		},
	}
	agent.taskTool = agentbuiltin.NewTaskTool(
		agentbuiltin.DefaultSubAgents(),
		func(ctx context.Context, spec agentbuiltin.SubAgentSpec, rt *agentbuiltin.SubAgentRuntime, prompt string) (string, error) {
			return "", nil
		},
		nil,
	)
	agent.SetChannelSenders(ChannelSendersConfig{AllowedRoots: []string{t.TempDir()}})

	names := toolNames(t, agent.toolsForChat(context.Background(), "", "", "a2a"))

	for _, want := range []string{"webfetch", "websearch", "cyeam_public"} {
		if !names[want] {
			t.Fatalf("A2A tools missing %q; got %#v", want, names)
		}
	}
	for _, blocked := range []string{
		"ask_user_question",
		"memory_save",
		"memory_forget",
		"memory_list",
		"task",
		"channel_send",
		"log_search",
		"amap_weather",
		"github_repo",
	} {
		if names[blocked] {
			t.Fatalf("A2A tools should not include %q; got %#v", blocked, names)
		}
	}
}

func TestA2ASubAgentToolsOnlyExposeCYEAMMCP(t *testing.T) {
	agent := &Agent{
		cfg: Config{BuiltinWebFetchEnabled: true},
		extMCPNames: []string{
			"CYEAM",
			"AMap",
			"github",
		},
		extToolSets: [][]tool.BaseTool{
			{testTool{name: "cyeam_public"}},
			{testTool{name: "amap_weather"}},
			{testTool{name: "github_repo"}},
		},
	}

	names := toolNames(t, agent.subAgentTools(context.Background(), true, "a2a"))
	if !names["cyeam_public"] {
		t.Fatalf("A2A subagent tools missing CYEAM tool; got %#v", names)
	}
	for _, blocked := range []string{"amap_weather", "github_repo"} {
		if names[blocked] {
			t.Fatalf("A2A subagent tools should not include %q; got %#v", blocked, names)
		}
	}
}
