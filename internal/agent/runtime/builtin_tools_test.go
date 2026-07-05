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
)

type testTool struct {
	name string
}

func TestAssistantResultUsesAskMessageInsteadOfGenericFallback(t *testing.T) {
	ask := &agentbuiltin.AskData{
		Question: "是否允许执行命令：git status",
		Options:  []string{"允许::执行该命令", "拒绝::不执行"},
		BashHash: "abc123",
	}

	msg := assistantResultAfterRun(nil, "", ask)
	if msg == nil {
		t.Fatal("assistantResultAfterRun() returned nil")
	}
	if strings.Contains(msg.Content, "命令或工具已执行完成") {
		t.Fatalf("assistantResultAfterRun() content = %q, want ask-specific message", msg.Content)
	}
	if !strings.Contains(msg.Content, "确认") {
		t.Fatalf("assistantResultAfterRun() content = %q, want confirmation prompt", msg.Content)
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
