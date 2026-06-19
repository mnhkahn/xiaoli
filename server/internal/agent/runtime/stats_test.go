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

func toolStatsCalls(rec *Recorder, name string) int64 {
	if rec.toolStats == nil {
		return 0
	}
	entry := rec.toolStats[name]
	if entry == nil {
		return 0
	}
	return entry.Calls
}

func toolStatsErrors(rec *Recorder, name string) int64 {
	if rec.toolStats == nil {
		return 0
	}
	entry := rec.toolStats[name]
	if entry == nil {
		return 0
	}
	return entry.Errors
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
	rec.toolStats = make(map[string]*toolStatsEntry)

	ft := &fakeInvokableTool{name: "my_tool", failOn: 0}
	agent := &Agent{recorder: rec}

	wrapped := agent.WrapTool(ft, "builtin")
	inv, ok := wrapped.(tool.InvokableTool)
	if !ok {
		t.Fatal("wrapped should be InvokableTool")
	}

	inv.InvokableRun(context.Background(), `{}`)
	inv.InvokableRun(context.Background(), `{}`)

	if c := toolStatsCalls(rec, "my_tool"); c != 2 {
		t.Fatalf("tool my_tool calls = %d, want 2", c)
	}
	if e := toolStatsErrors(rec, "my_tool"); e != 0 {
		t.Fatalf("tool my_tool errors = %d, want 0", e)
	}
}

func TestWrapToolCountsErrors(t *testing.T) {
	rec := GlobalRecorder()
	rec.toolStats = make(map[string]*toolStatsEntry)

	ft := &fakeInvokableTool{name: "err_tool", failOn: 3}
	agent := &Agent{recorder: rec}

	wrapped := agent.WrapTool(ft, "mcp")
	inv, ok := wrapped.(tool.InvokableTool)
	if !ok {
		t.Fatal("wrapped should be InvokableTool")
	}

	inv.InvokableRun(context.Background(), `{}`)
	inv.InvokableRun(context.Background(), `{}`)
	inv.InvokableRun(context.Background(), `{}`)

	if c := toolStatsCalls(rec, "err_tool"); c != 3 {
		t.Fatalf("tool err_tool calls = %d, want 3", c)
	}
	if e := toolStatsErrors(rec, "err_tool"); e != 1 {
		t.Fatalf("tool err_tool errors = %d, want 1", e)
	}
}

func TestWrapToolPerToolName(t *testing.T) {
	rec := GlobalRecorder()
	rec.toolStats = make(map[string]*toolStatsEntry)

	agent := &Agent{recorder: rec}

	alpha := &fakeInvokableTool{name: "alpha"}
	beta := &fakeInvokableTool{name: "beta"}

	w1 := agent.WrapTool(alpha, "")
	w2 := agent.WrapTool(beta, "")

	w1.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
	w1.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
	w2.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)

	if c := toolStatsCalls(rec, "alpha"); c != 2 {
		t.Fatalf("alpha calls = %d, want 2", c)
	}
	if c := toolStatsCalls(rec, "beta"); c != 1 {
		t.Fatalf("beta calls = %d, want 1", c)
	}
}