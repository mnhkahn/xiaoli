package runtime

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
)

func TestCommitToolOnlyAvailableInTUI(t *testing.T) {
	agent := &Agent{}
	if !hasToolNamed(t, agent.toolsForChat(context.Background(), "session", "local", "tui"), "commit") {
		t.Fatal("tui tools do not include commit")
	}
	if hasToolNamed(t, agent.toolsForChat(context.Background(), "session", "device", "lark"), "commit") {
		t.Fatal("non-tui tools unexpectedly include commit")
	}
}

func TestCommitRequestConsumedOnce(t *testing.T) {
	agent := &Agent{}
	agent.storeCommitRequest("local", &agentbuiltin.CommitRequest{})
	if agent.ConsumeCommitRequest("local") == nil {
		t.Fatal("first ConsumeCommitRequest() returned nil")
	}
	if agent.ConsumeCommitRequest("local") != nil {
		t.Fatal("second ConsumeCommitRequest() returned the consumed request")
	}
}

func hasToolNamed(t *testing.T, tools []tool.BaseTool, name string) bool {
	t.Helper()
	for _, candidate := range tools {
		info, err := candidate.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name == name {
			return true
		}
	}
	return false
}
