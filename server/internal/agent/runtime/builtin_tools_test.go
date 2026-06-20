package runtime

import (
	"context"
	"testing"

	agentbuiltin "xiaoli/server/internal/agent/tool/builtin"
)

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

func TestSubAgentToolsNotIncludesInteractiveTools(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	tools := agent.subAgentTools(context.Background(), true)
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
	tools := agent.subAgentTools(context.Background(), false)
	if len(tools) != 0 {
		t.Fatalf("subAgentTools(deny) should be empty, got %d", len(tools))
	}
}
