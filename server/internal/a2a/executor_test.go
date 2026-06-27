package a2a

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Mock conversation pipeline that returns predefined response
type mockPipeline struct {
	response string
	err      error
}

func (m *mockPipeline) Run(ctx context.Context, turn ConversationTurn) (ConversationReply, error) {
	return ConversationReply{Text: m.response}, m.err
}

func TestExecutor_SendMessage(t *testing.T) {
	pipeline := &mockPipeline{response: "今天北京晴天，25度"}
	store := NewMemoryTaskStore(60)
	executor := NewExecutor(pipeline, store, 2000)

	req := &SendMessageRequest{
		Message: Message{
			Content: []ContentPart{{Type: "text", Text: "北京天气"}},
		},
	}

	resp, err := executor.SendMessage(context.Background(), req)
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.TaskID)
	assert.Equal(t, "completed", string(resp.Status))
	assert.Equal(t, "今天北京晴天，25度", resp.Result)

	// Verify task stored
	task, ok := store.Get(resp.TaskID)
	assert.True(t, ok)
	assert.Equal(t, "今天北京晴天，25度", task.Result)
}

func TestExecutor_SendMessage_EmptyText(t *testing.T) {
	pipeline := &mockPipeline{}
	store := NewMemoryTaskStore(60)
	executor := NewExecutor(pipeline, store, 2000)

	req := &SendMessageRequest{
		Message: Message{
			Content: []ContentPart{{Type: "text", Text: ""}},
		},
	}

	_, err := executor.SendMessage(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestExecutor_SendMessage_InputTooLong(t *testing.T) {
	pipeline := &mockPipeline{}
	store := NewMemoryTaskStore(60)
	executor := NewExecutor(pipeline, store, 10) // Max 10 chars

	longText := "this is way more than ten characters"
	req := &SendMessageRequest{
		Message: Message{
			Content: []ContentPart{{Type: "text", Text: longText}},
		},
	}

	_, err := executor.SendMessage(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too long")
}

func TestExecutor_GetTask(t *testing.T) {
	pipeline := &mockPipeline{}
	store := NewMemoryTaskStore(60)
	executor := NewExecutor(pipeline, store, 2000)

	// Put a task directly
	store.Put("task_abc", &Task{ID: "task_abc", Status: TaskStatusCompleted, Result: "test result"})

	req := &GetTaskRequest{TaskID: "task_abc"}
	resp, err := executor.GetTask(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "task_abc", resp.TaskID)
	assert.Equal(t, "completed", string(resp.Status))
	assert.Equal(t, "test result", resp.Result)
}

func TestExecutor_GetTask_NotFound(t *testing.T) {
	pipeline := &mockPipeline{}
	store := NewMemoryTaskStore(60)
	executor := NewExecutor(pipeline, store, 2000)

	req := &GetTaskRequest{TaskID: "nonexistent"}
	_, err := executor.GetTask(context.Background(), req)
	assert.Error(t, err)
}
