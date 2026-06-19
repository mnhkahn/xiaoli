package runtime

import (
	"context"
	"testing"
)

func TestToolsForChatIncludesConfiguredBuiltinTools(t *testing.T) {
	agent := &Agent{cfg: Config{BuiltinWebFetchEnabled: true}}
	tools := agent.toolsForChat(context.Background(), "", "")
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Name != "webfetch" {
		t.Fatalf("tool name = %q, want webfetch", info.Name)
	}
}
