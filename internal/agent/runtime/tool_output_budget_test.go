package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestToolOutputBudgetCompactsOldestToolResults(t *testing.T) {
	store := newToolOutputStore(t.TempDir())
	old := strings.Repeat("old", 5000)
	newest := strings.Repeat("new", 5000)
	if _, err := store.store(old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.store(newest); err != nil {
		t.Fatal(err)
	}
	mw := &toolOutputBudgetMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store, maxBytes: len(newest) + 200}
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.ToolMessage(old, "call-old"), schema.ToolMessage(newest, "call-new")}}
	_, state, err := mw.BeforeModelRewriteState(context.Background(), state, &adk.ModelContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Messages[0].Content, "output_id=") || state.Messages[1].Content != newest {
		t.Fatalf("messages were not compacted oldest-first: %#v", state.Messages)
	}
}
