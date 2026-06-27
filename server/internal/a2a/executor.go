package a2a

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/mnhkahn/gogogo/logger"
)

// ConversationReply is the pipeline's response for an A2A turn.
type ConversationReply struct {
	Text string
}

// ConversationTurn describes a single A2A request routed to the agent pipeline.
// ConversationID carries the internal session ID of the form
// "a2a:<key_id>:<context_id>" so the pipeline cannot be tricked into
// addressing another caller's memory or session.
type ConversationTurn struct {
	Channel        string
	ConversationID string
	Text           string
	UseDeviceTools bool
}

// ConversationPipeline runs an A2A turn and returns the agent's reply.
type ConversationPipeline interface {
	Run(ctx context.Context, turn ConversationTurn) (ConversationReply, error)
}

// Executor implements a2asrv.AgentExecutor. It validates inbound text,
// routes the turn through the A2A-dedicated subagent pipeline, and emits
// A2A events that the a2a-go library translates into task state.
type Executor struct {
	pipeline      ConversationPipeline
	maxInputChars int
}

var _ a2asrv.AgentExecutor = (*Executor)(nil)

// NewExecutor creates an A2A executor. The task store is owned by the
// a2a-go RequestHandler; the executor only needs the pipeline and input limit.
func NewExecutor(pipeline ConversationPipeline, maxInputChars int) *Executor {
	return &Executor{
		pipeline:      pipeline,
		maxInputChars: maxInputChars,
	}
}

// Execute validates the inbound message, creates a submitted task, runs the
// pipeline, and emits a terminal status event. Input validation failures are
// returned as errors before any task is created, so the client receives a
// JSON-RPC error rather than a task_id.
func (e *Executor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		keyID, _ := authenticator(ctx)
		msgLen := 0
		if execCtx.Message != nil {
			msgLen = len(extractText(execCtx.Message.Parts))
		}
		logger.Infof("[A2A] request key=%s context_id=%s msg_len=%d", keyID, execCtx.ContextID, msgLen)

		if execCtx.Message == nil {
			yield(nil, fmt.Errorf("%w: message is required", a2a.ErrInvalidParams))
			return
		}

		if !validateParts(execCtx.Message.Parts) {
			yield(nil, fmt.Errorf("%w: only text parts are supported", a2a.ErrInvalidParams))
			return
		}

		text := strings.TrimSpace(extractText(execCtx.Message.Parts))
		if text == "" {
			yield(nil, fmt.Errorf("%w: message text is empty", a2a.ErrInvalidParams))
			return
		}
		if len(text) > e.maxInputChars {
			yield(nil, fmt.Errorf("%w: message text exceeds %d characters", a2a.ErrInvalidParams, e.maxInputChars))
			return
		}

		if execCtx.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
				return
			}
		}
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		sessionID := "a2a:" + keyID + ":" + execCtx.ContextID
		turn := ConversationTurn{
			Channel:        "a2a",
			ConversationID: sessionID,
			Text:           text,
			UseDeviceTools: false,
		}

		reply, err := e.pipeline.Run(ctx, turn)
		if err != nil {
			logger.Infof("[A2A] pipeline failed key=%s context_id=%s err=%v", keyID, execCtx.ContextID, err)
			failMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("处理失败，请稍后重试"))
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, failMsg), nil)
			return
		}

		resultMsg := a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewTextPart(reply.Text))
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, resultMsg), nil)
	}
}

// Cancel is not supported. Returns unsupported error immediately.
func (e *Executor) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(nil, a2a.ErrUnsupportedOperation)
	}
}

// validateParts returns false if any part is not a text part. A2A input is
// text-only; binary, data, and file URL parts are rejected.
func validateParts(parts a2a.ContentParts) bool {
	if len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		if p == nil {
			return false
		}
		if _, ok := p.Content.(a2a.Text); !ok {
			return false
		}
	}
	return true
}

// extractText joins all text parts into a single string.
func extractText(parts a2a.ContentParts) string {
	var texts []string
	for _, p := range parts {
		if t, ok := p.Content.(a2a.Text); ok && t != "" {
			texts = append(texts, string(t))
		}
	}
	return strings.Join(texts, "\n")
}
