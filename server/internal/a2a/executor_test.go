package a2a

import (
	"context"
	"iter"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/stretchr/testify/assert"
)

type mockPipeline struct {
	response string
	err      error
}

func (m *mockPipeline) Run(ctx context.Context, turn ConversationTurn) (ConversationReply, error) {
	if m.err != nil {
		return ConversationReply{}, m.err
	}
	return ConversationReply{Text: m.response}, nil
}

func collectEvents(seq iter.Seq2[a2a.Event, error]) ([]a2a.Event, error) {
	var events []a2a.Event
	var firstErr error
	for ev, err := range seq {
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if ev != nil {
			events = append(events, ev)
		}
	}
	return events, firstErr
}

func TestExecutor_Execute_Success(t *testing.T) {
	pipeline := &mockPipeline{response: "今天北京晴天，25度"}
	executor := NewExecutor(pipeline, 2000)

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("北京天气"))
	execCtx := &a2asrv.ExecutorContext{
		Message:   msg,
		TaskID:    "task_test1",
		ContextID: "ctx_test1",
	}

	events, err := collectEvents(executor.Execute(context.Background(), execCtx))
	assert.NoError(t, err)
	// Expect: submitted task, working status, completed status
	assert.GreaterOrEqual(t, len(events), 2)

	var lastEvent a2a.Event
	for _, ev := range events {
		lastEvent = ev
	}
	completed, ok := lastEvent.(*a2a.TaskStatusUpdateEvent)
	assert.True(t, ok)
	assert.Equal(t, a2a.TaskStateCompleted, completed.Status.State)
}

func TestExecutor_Execute_EmptyText(t *testing.T) {
	pipeline := &mockPipeline{}
	executor := NewExecutor(pipeline, 2000)

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(""))
	execCtx := &a2asrv.ExecutorContext{
		Message:   msg,
		TaskID:    "task_empty",
		ContextID: "ctx_empty",
	}

	events, err := collectEvents(executor.Execute(context.Background(), execCtx))
	assert.Error(t, err)
	assert.Empty(t, events)
}

func TestExecutor_Execute_InputTooLong(t *testing.T) {
	pipeline := &mockPipeline{}
	executor := NewExecutor(pipeline, 10)

	longText := "this is way more than ten characters"
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(longText))
	execCtx := &a2asrv.ExecutorContext{
		Message:   msg,
		TaskID:    "task_long",
		ContextID: "ctx_long",
	}

	events, err := collectEvents(executor.Execute(context.Background(), execCtx))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
	assert.Empty(t, events)
}

func TestExecutor_Execute_NonTextRejected(t *testing.T) {
	pipeline := &mockPipeline{}
	executor := NewExecutor(pipeline, 2000)

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"key": "value"}))
	execCtx := &a2asrv.ExecutorContext{
		Message:   msg,
		TaskID:    "task_data",
		ContextID: "ctx_data",
	}

	events, err := collectEvents(executor.Execute(context.Background(), execCtx))
	assert.Error(t, err)
	assert.Empty(t, events)
}

func TestExecutor_Execute_PipelineFailureEmitsFailedStatus(t *testing.T) {
	pipeline := &mockPipeline{err: context.DeadlineExceeded}
	executor := NewExecutor(pipeline, 2000)

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))
	execCtx := &a2asrv.ExecutorContext{
		Message:   msg,
		TaskID:    "task_fail",
		ContextID: "ctx_fail",
	}

	events, err := collectEvents(executor.Execute(context.Background(), execCtx))
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(events), 2)

	var lastEvent a2a.Event
	for _, ev := range events {
		lastEvent = ev
	}
	failed, ok := lastEvent.(*a2a.TaskStatusUpdateEvent)
	assert.True(t, ok)
	assert.Equal(t, a2a.TaskStateFailed, failed.Status.State)
}

func TestExecutor_Cancel_ReturnsUnsupported(t *testing.T) {
	pipeline := &mockPipeline{}
	executor := NewExecutor(pipeline, 2000)

	execCtx := &a2asrv.ExecutorContext{TaskID: "task_cancel"}
	events, err := collectEvents(executor.Cancel(context.Background(), execCtx))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
	assert.Empty(t, events)
}
