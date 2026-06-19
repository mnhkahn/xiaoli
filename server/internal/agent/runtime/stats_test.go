package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type fakeInvokableTool struct {
	name     string
	failOn   int
	callSeq  int
}

func (f *fakeInvokableTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: f.name}, nil
}

func (f *fakeInvokableTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	f.callSeq++
	if f.failOn > 0 && f.callSeq >= f.failOn {
		return "", errors.New("fake error")
	}
	return "ok", nil
}

func TestWrapToolNilRecorder(t *testing.T) {
	ft := &fakeInvokableTool{name: "test"}
	wrapped := (*Agent)(nil).WrapTool(ft, "builtin")
	if wrapped != ft {
		t.Fatal("nil agent should return tool unwrapped")
	}

	agent := &Agent{}
	wrapped = agent.WrapTool(ft, "builtin")
	if wrapped != ft {
		t.Fatal("agent with nil recorder should return tool unwrapped")
	}
}

func TestWrapToolNilRecorderInvokableRun(t *testing.T) {
	ft := &fakeInvokableTool{name: "test"}
	wrapped := (*Agent)(nil).WrapTool(ft, "builtin")

	inv, ok := wrapped.(tool.InvokableTool)
	if !ok {
		t.Fatal("wrapped tool should be InvokableTool")
	}
	r, err := inv.InvokableRun(context.Background(), `{"foo":"bar"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != "ok" {
		t.Fatalf("result = %q, want ok", r)
	}
}

func TestWrapToolCounts(t *testing.T) {
	rec := GlobalRecorder()
	rec.builtinCalls = 0
	rec.builtinErrors = 0

	ft := &fakeInvokableTool{name: "test", failOn: 0}
	agent := &Agent{recorder: rec}

	wrapped := agent.WrapTool(ft, "builtin")
	inv, ok := wrapped.(tool.InvokableTool)
	if !ok {
		t.Fatal("wrapped should be InvokableTool")
	}

	inv.InvokableRun(context.Background(), `{}`)
	inv.InvokableRun(context.Background(), `{}`)

	if rec.builtinCalls != 2 {
		t.Fatalf("builtinCalls = %d, want 2", rec.builtinCalls)
	}
	if rec.builtinErrors != 0 {
		t.Fatalf("builtinErrors = %d, want 0", rec.builtinErrors)
	}
}

func TestWrapToolCountsErrors(t *testing.T) {
	rec := GlobalRecorder()
	rec.mcpCalls = 0
	rec.mcpErrors = 0

	ft := &fakeInvokableTool{name: "test", failOn: 3}
	agent := &Agent{recorder: rec}

	wrapped := agent.WrapTool(ft, "mcp")
	inv, ok := wrapped.(tool.InvokableTool)
	if !ok {
		t.Fatal("wrapped should be InvokableTool")
	}

	inv.InvokableRun(context.Background(), `{}`)
	inv.InvokableRun(context.Background(), `{}`)
	inv.InvokableRun(context.Background(), `{}`)

	if rec.mcpCalls != 3 {
		t.Fatalf("mcpCalls = %d, want 3", rec.mcpCalls)
	}
	if rec.mcpErrors != 1 {
		t.Fatalf("mcpErrors = %d, want 1", rec.mcpErrors)
	}
}

func TestWrapToolSkillCategory(t *testing.T) {
	rec := GlobalRecorder()
	rec.skillCalls = 0
	rec.skillErrors = 0

	ft := &fakeInvokableTool{name: "skill-test"}
	agent := &Agent{recorder: rec}

	wrapped := agent.WrapTool(ft, "skill")
	inv, ok := wrapped.(tool.InvokableTool)
	if !ok {
		t.Fatal("wrapped should be InvokableTool")
	}

	inv.InvokableRun(context.Background(), `{}`)

	if rec.skillCalls != 1 {
		t.Fatalf("skillCalls = %d, want 1", rec.skillCalls)
	}
}