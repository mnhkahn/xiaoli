package a2a

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// --- Types matching a2asrv protocol ---

type ContentPart struct {
	Type string `json:"type"` // "text"
	Text string `json:"text,omitempty"`
}

type Message struct {
	Role    string        `json:"role"`
	Content []ContentPart `json:"content"`
}

type SendMessageRequest struct {
	Message   Message `json:"message"`
	ContextID string  `json:"contextId,omitempty"`
}

type SendMessageResponse struct {
	TaskID string     `json:"taskId"`
	Status TaskStatus `json:"status"`
	Result string     `json:"result,omitempty"`
	Error  string     `json:"error,omitempty"`
}

type GetTaskRequest struct {
	TaskID string `json:"taskId"`
}

type GetTaskResponse struct {
	TaskID string     `json:"taskId"`
	Status TaskStatus `json:"status"`
	Result string     `json:"result,omitempty"`
	Error  string     `json:"error,omitempty"`
}

// --- Conversation Pipeline interface (matches admin.ConversationPipeline) ---

type ConversationReply struct {
	Text string
}

type ConversationTurn struct {
	Channel        string
	ConversationID string
	DeviceID       string
	Text           string
	UseDeviceTools bool
}

type ConversationPipeline interface {
	Run(ctx context.Context, turn ConversationTurn) (ConversationReply, error)
}

// --- Executor ---

// Executor implements the A2A agent execution logic
type Executor struct {
	pipeline      ConversationPipeline
	taskStore     TaskStore
	maxInputChars int
}

// NewExecutor creates a new A2A executor
func NewExecutor(pipeline ConversationPipeline, store TaskStore, maxInputChars int) *Executor {
	return &Executor{
		pipeline:      pipeline,
		taskStore:     store,
		maxInputChars: maxInputChars,
	}
}

// SendMessage handles A2A SendMessage request synchronously
func (e *Executor) SendMessage(ctx context.Context, req *SendMessageRequest) (*SendMessageResponse, error) {
	taskID := generateTaskID()

	task := &Task{
		ID:        taskID,
		Status:    TaskStatusWorking,
		CreatedAt: time.Now(),
	}
	e.taskStore.Put(taskID, task)

	// Extract and validate text content
	text := extractText(req.Message.Content)
	text = strings.TrimSpace(text)

	if text == "" {
		task.Status = TaskStatusFailed
		task.Error = "empty message text"
		e.taskStore.Put(taskID, task)
		return nil, errors.New("empty message text")
	}

	if len(text) > e.maxInputChars {
		task.Status = TaskStatusFailed
		task.Error = "message text too long"
		e.taskStore.Put(taskID, task)
		return nil, errors.New("message text too long")
	}

	// Get key_id from context for logging/identification
	keyID := ""
	if k, ok := ctx.Value(keyIDContextKey).(string); ok {
		keyID = k
	}

	// Build conversation turn for A2A channel
	turn := ConversationTurn{
		Channel:        "a2a",
		ConversationID: "", // No long-term memory for A2A
		DeviceID:       keyID,
		Text:           text,
		UseDeviceTools: false, // No device tools for A2A
	}

	// Execute synchronously
	reply, err := e.pipeline.Run(ctx, turn)
	if err != nil {
		task.Status = TaskStatusFailed
		task.Error = "processing failed"
		e.taskStore.Put(taskID, task)
		return nil, err
	}

	task.Status = TaskStatusCompleted
	task.Result = reply.Text
	e.taskStore.Put(taskID, task)

	return &SendMessageResponse{
		TaskID: taskID,
		Status: TaskStatusCompleted,
		Result: reply.Text,
	}, nil
}

// GetTask retrieves task status and result
func (e *Executor) GetTask(ctx context.Context, req *GetTaskRequest) (*GetTaskResponse, error) {
	task, ok := e.taskStore.Get(req.TaskID)
	if !ok {
		return nil, errors.New("task not found")
	}

	return &GetTaskResponse{
		TaskID: task.ID,
		Status: task.Status,
		Result: task.Result,
		Error:  task.Error,
	}, nil
}

// extractText combines all text parts into a single string
func extractText(parts []ContentPart) string {
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// generateTaskID creates a random task identifier
func generateTaskID() string {
	b := make([]byte, 12)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback if rand fails (should never happen)
		return "task_" + time.Now().Format("20060102150405")
	}
	return "task_" + hex.EncodeToString(b)
}
