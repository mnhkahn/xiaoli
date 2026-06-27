package runtime

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	agentbuiltin "xiaoli/server/internal/agent/tool/builtin"
)

type testTool struct {
	name string
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
	found := false
	for _, tb := range tools {
		info, err := tb.Info(context.Background())
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if info.Name == "channel_send" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("toolsForChat should include channel_send when channel senders are configured")
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
		if info.Name == "ask_user_question" || info.Name == "task" || info.Name == "memory_save" || info.Name == "memory_forget" || info.Name == "memory_list" {
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
