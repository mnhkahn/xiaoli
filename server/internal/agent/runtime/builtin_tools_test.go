package runtime

import (
	"context"
	"testing"
)

func TestToolsForChatIncludesConfiguredBuiltinTools(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	tools := agent.toolsForChat(context.Background(), "", "", "")
	if len(tools) != 4 {
		t.Fatalf("tools len = %d, want 4 (webfetch + websearch + task + ask)", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Name != "webfetch" {
		t.Fatalf("tool name = %q, want webfetch", info.Name)
	}
	info2, err := tools[1].Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info2.Name != "websearch" {
		t.Fatalf("tool name = %q, want websearch", info2.Name)
	}
	info3, err := tools[2].Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info3.Name != "task" {
		t.Fatalf("tool name = %q, want task", info3.Name)
	}
	info4, err := tools[3].Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info4.Name != "ask_user_question" {
		t.Fatalf("tool name = %q, want ask_user_question", info4.Name)
	}
}
