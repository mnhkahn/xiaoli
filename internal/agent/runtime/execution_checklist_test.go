package runtime

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestHasExecutionChecklist(t *testing.T) {
	if !hasExecutionChecklist(nil, "一次执行清单\n1. 写 plist\n2. 启动服务") {
		t.Fatal("explicit checklist was not detected")
	}
	if !hasExecutionChecklist([]*schema.Message{schema.UserMessage("1. 写 plist\n2. 启动服务")}, "继续") {
		t.Fatal("checklist in history was not detected")
	}
	if hasExecutionChecklist(nil, "1. 写 plist") {
		t.Fatal("single numbered item should not activate the guard")
	}
}

func TestShouldContinueExecutionChecklist(t *testing.T) {
	history := []*schema.Message{schema.UserMessage("一次执行清单\n1. 写 plist\n2. 启动服务")}
	if !shouldContinueExecutionChecklist(context.Background(), history, "继续", 1, nil, nil, nil) {
		t.Fatal("expected checklist continuation")
	}
	if shouldContinueExecutionChecklist(context.Background(), history, "继续", 0, nil, nil, nil) {
		t.Fatal("must not continue without a completed tool operation")
	}
	ctx := context.WithValue(context.Background(), executionChecklistContinuationKey{}, maxExecutionChecklistContinuations)
	if shouldContinueExecutionChecklist(ctx, history, "继续", 1, nil, nil, nil) {
		t.Fatal("must stop at the continuation limit")
	}
}

func TestToolRunTracker(t *testing.T) {
	ctx, tracker := withToolRunTracker(context.Background())
	recordToolRun(ctx)
	recordToolRun(ctx)
	if got := tracker.Count(); got != 2 {
		t.Fatalf("tracker count = %d, want 2", got)
	}
}
