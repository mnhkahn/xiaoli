package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type fakeInvokableTool struct {
	name    string
	failOn  int
	callSeq int
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

func firstBucket(rec *Recorder, mid string) *minuteBucket {
	for _, b := range rec.buckets[mid] {
		return b
	}
	return &minuteBucket{}
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

func TestWrapToolSkipsConsecutiveDuplicateCalls(t *testing.T) {
	rec := GlobalRecorder()
	rec.toolStats = make(map[string]*toolStatsEntry)

	ft := &fakeInvokableTool{name: "repeatable"}
	agent := &Agent{recorder: rec}
	inv := agent.WrapTool(ft, "builtin").(tool.InvokableTool)
	ctx := withToolDuplicateGuard(context.Background())

	first, err := inv.InvokableRun(ctx, `{"b":2,"a":1}`)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	second, err := inv.InvokableRun(ctx, `{"a":1,"b":2}`)
	if err != nil {
		t.Fatalf("duplicate call error: %v", err)
	}

	if first != "ok" {
		t.Fatalf("first result = %q, want ok", first)
	}
	if !strings.Contains(second, "skipped duplicate tool call") {
		t.Fatalf("duplicate result = %q, want skipped duplicate message", second)
	}
	if ft.callSeq != 1 {
		t.Fatalf("underlying calls = %d, want 1", ft.callSeq)
	}
	if c := toolStatsCalls(rec, "repeatable"); c != 2 {
		t.Fatalf("tool stats calls = %d, want 2", c)
	}
}

func TestWrapToolAllowsSameCallAfterDifferentTool(t *testing.T) {
	rec := GlobalRecorder()
	rec.toolStats = make(map[string]*toolStatsEntry)

	alpha := &fakeInvokableTool{name: "alpha"}
	beta := &fakeInvokableTool{name: "beta"}
	agent := &Agent{recorder: rec}
	alphaInv := agent.WrapTool(alpha, "builtin").(tool.InvokableTool)
	betaInv := agent.WrapTool(beta, "builtin").(tool.InvokableTool)
	ctx := withToolDuplicateGuard(context.Background())

	alphaInv.InvokableRun(ctx, `{"path":"a.go"}`)
	betaInv.InvokableRun(ctx, `{}`)
	alphaInv.InvokableRun(ctx, `{"path":"a.go"}`)

	if alpha.callSeq != 2 {
		t.Fatalf("alpha underlying calls = %d, want 2", alpha.callSeq)
	}
	if beta.callSeq != 1 {
		t.Fatalf("beta underlying calls = %d, want 1", beta.callSeq)
	}
}

func TestTraceTopMessageStatsShowsLargestMessages(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.User, Content: strings.Repeat("u", 4)},
		{Role: schema.Tool, Name: "news", ToolCallID: "call-news", Content: strings.Repeat("n", 10)},
		{Role: schema.Assistant, Content: strings.Repeat("a", 7)},
		{Role: schema.System, Content: strings.Repeat("s", 2)},
	}

	got := traceTopMessageStats(msgs, 3)
	want := "[#2 role=tool name=news len=10,#3 role=assistant len=7,#1 role=user len=4]"
	if got != want {
		t.Fatalf("traceTopMessageStats() = %q, want %q", got, want)
	}
}

func TestRecordCachedTokens(t *testing.T) {
	rec := &Recorder{buckets: make(map[string]map[string]*minuteBucket)}

	mid := "test-model"

	rec.record(mid, &model.CallbackOutput{
		TokenUsage: &model.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			PromptTokenDetails: model.PromptTokenDetails{
				CachedTokens: 30,
			},
		},
	})
	b := firstBucket(rec, mid)
	if b.cachedPromptTokens != 30 {
		t.Fatalf("cached = %d, want 30", b.cachedPromptTokens)
	}
	if b.promptTokens != 100 {
		t.Fatalf("prompt = %d, want 100", b.promptTokens)
	}
}

func TestRecordCachedTokensClamp(t *testing.T) {
	rec := &Recorder{buckets: make(map[string]map[string]*minuteBucket)}

	mid := "test-model"

	rec.record(mid, &model.CallbackOutput{
		TokenUsage: &model.TokenUsage{
			PromptTokens:     50,
			CompletionTokens: 25,
			PromptTokenDetails: model.PromptTokenDetails{
				CachedTokens: 999,
			},
		},
	})
	b := firstBucket(rec, mid)
	if b.cachedPromptTokens != 50 {
		t.Fatalf("cached clamped = %d, want 50 (prompt=50)", b.cachedPromptTokens)
	}
}

func TestRecordCachedTokensNegative(t *testing.T) {
	rec := &Recorder{buckets: make(map[string]map[string]*minuteBucket)}

	mid := "test-model"

	rec.record(mid, &model.CallbackOutput{
		TokenUsage: &model.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			PromptTokenDetails: model.PromptTokenDetails{
				CachedTokens: -10,
			},
		},
	})
	b := firstBucket(rec, mid)
	if b.cachedPromptTokens != 0 {
		t.Fatalf("cached negative = %d, want 0", b.cachedPromptTokens)
	}
}

func TestRecordCachedTokensAccumulate(t *testing.T) {
	rec := &Recorder{buckets: make(map[string]map[string]*minuteBucket)}

	mid := "test-model"

	for i := 0; i < 3; i++ {
		rec.record(mid, &model.CallbackOutput{
			TokenUsage: &model.TokenUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
				PromptTokenDetails: model.PromptTokenDetails{
					CachedTokens: 20,
				},
			},
		})
	}

	if b := firstBucket(rec, mid); b.cachedPromptTokens != 60 {
		t.Fatalf("cached accumulated = %d, want 60", b.cachedPromptTokens)
	}
}

func TestStatusShowsCachedTokens(t *testing.T) {
	rec := &Recorder{buckets: make(map[string]map[string]*minuteBucket)}

	mid := "test-model"
	for i := 0; i < 3; i++ {
		rec.record(mid, &model.CallbackOutput{
			TokenUsage: &model.TokenUsage{
				PromptTokens:     200,
				CompletionTokens: 100,
				PromptTokenDetails: model.PromptTokenDetails{
					CachedTokens: 50,
				},
			},
		})
	}

	output := rec.Status(StatusOptions{})
	if !strings.Contains(output, "cached 150") {
		t.Fatal(fmt.Sprintf("output should contain 'cached 150', got: %s", output))
	}
	if !strings.Contains(output, "hit 25.0%") {
		t.Fatal(fmt.Sprintf("output should contain 'hit 25.0%%', got: %s", output))
	}
}

func TestStatusContextSection(t *testing.T) {
	rec := &Recorder{buckets: make(map[string]map[string]*minuteBucket)}

	output := rec.Status(StatusOptions{
		Context: &ContextUsage{
			Model:          "test-llm",
			ContextLength:  131072,
			MaxTokens:      8192,
			EstimatedInput: 50000,
			CompressAt:     90000,
		},
	})
	if !strings.Contains(output, "test-llm") {
		t.Fatal(fmt.Sprintf("output should contain model name, got: %s", output))
	}
	if !strings.Contains(output, "131072") {
		t.Fatal(fmt.Sprintf("output should contain context length, got: %s", output))
	}
	if !strings.Contains(output, "50000") {
		t.Fatal(fmt.Sprintf("output should contain estimated input, got: %s", output))
	}
	if !strings.Contains(output, "8192") {
		t.Fatal(fmt.Sprintf("output should contain max tokens, got: %s", output))
	}
	if !strings.Contains(output, "90000") {
		t.Fatal(fmt.Sprintf("output should contain compress threshold, got: %s", output))
	}
	pct := fmt.Sprintf("%.1f%%", float64(50000)/float64(131072)*100)
	if !strings.Contains(output, pct) {
		t.Fatal(fmt.Sprintf("output should contain %s, got: %s", pct, output))
	}
}
