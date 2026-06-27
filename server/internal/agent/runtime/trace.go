package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
)

type traceContextKey struct{}

type TraceOptions struct {
	Enabled        bool
	LogInputs      bool
	LogOutputs     bool
	MaxValueLength int
}

type traceState struct {
	mu       sync.Mutex
	TraceID  string
	Profile  string
	KeyID    string
	Context  string
	Options  TraceOptions
	modelSeq int
	toolSeq  int
}

type traceRunKey struct{}

type traceRun struct {
	Step  int
	Start time.Time
}

func newA2ATraceOptions(cfg Config) TraceOptions {
	maxLen := cfg.A2ATraceMaxValueLength
	if maxLen <= 0 {
		maxLen = 800
	}
	return TraceOptions{
		Enabled:        cfg.A2ATraceEnabled,
		LogInputs:      cfg.A2ATraceLogInputs,
		LogOutputs:     cfg.A2ATraceLogOutputs,
		MaxValueLength: maxLen,
	}
}

func withTrace(ctx context.Context, st *traceState) context.Context {
	if st == nil || !st.Options.Enabled {
		return ctx
	}
	return context.WithValue(ctx, traceContextKey{}, st)
}

func traceFromContext(ctx context.Context) *traceState {
	st, _ := ctx.Value(traceContextKey{}).(*traceState)
	if st == nil || !st.Options.Enabled {
		return nil
	}
	return st
}

func newA2ATraceState(profile, sessionID string, opts TraceOptions) *traceState {
	if !opts.Enabled {
		return nil
	}
	keyID, contextID := splitA2ASessionID(sessionID)
	traceID := fmt.Sprintf("a2a:%s:%s:%s:%d", keyID, contextID, profile, time.Now().UnixNano())
	return &traceState{
		TraceID: traceID,
		Profile: profile,
		KeyID:   keyID,
		Context: contextID,
		Options: opts,
	}
}

func splitA2ASessionID(sessionID string) (string, string) {
	parts := strings.SplitN(sessionID, ":", 3)
	if len(parts) == 3 && parts[0] == "a2a" {
		return parts[1], parts[2]
	}
	return "", sessionID
}

func (s *traceState) nextModelRun() traceRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelSeq++
	return traceRun{Step: s.modelSeq, Start: time.Now()}
}

func (s *traceState) nextToolStep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolSeq++
	return s.toolSeq
}

func tracePrefix(st *traceState) string {
	if st == nil {
		return "[A2A][trace]"
	}
	return fmt.Sprintf("[A2A][trace] trace_id=%s key=%s context_id=%s profile=%s", st.TraceID, st.KeyID, st.Context, st.Profile)
}

func traceTruncate(s string, maxLen int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", "\\n"))
	if maxLen <= 0 {
		maxLen = 800
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}

func traceToolNames(ctx context.Context, tools []tool.BaseTool) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		info, err := t.Info(ctx)
		if err != nil || info == nil || info.Name == "" {
			continue
		}
		names = append(names, info.Name)
	}
	sortStrings(names)
	return names
}

func traceMessageSummary(msg *schema.Message) (role schema.RoleType, contentLen int, toolCalls []string, toolName string) {
	if msg == nil {
		return "", 0, nil, ""
	}
	toolCalls = make([]string, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		name := call.Function.Name
		if name == "" {
			name = call.ID
		}
		if name != "" {
			toolCalls = append(toolCalls, name)
		}
	}
	return msg.Role, len(msg.Content), toolCalls, msg.ToolName
}

func logTraceEvent(ctx context.Context, index int, event *adk.AgentEvent) {
	st := traceFromContext(ctx)
	if st == nil || event == nil {
		return
	}
	if event.Err != nil {
		logger.Infof("%s event step=%d agent=%s err=%v", tracePrefix(st), index, event.AgentName, event.Err)
		return
	}
	if event.Action != nil {
		logger.Infof("%s event step=%d agent=%s action exit=%v transfer=%v break=%v", tracePrefix(st), index, event.AgentName, event.Action.Exit, event.Action.TransferToAgent != nil, event.Action.BreakLoop != nil)
		return
	}
	if event.Output == nil || event.Output.MessageOutput == nil {
		logger.Infof("%s event step=%d agent=%s empty_output=true", tracePrefix(st), index, event.AgentName)
		return
	}
	mv := event.Output.MessageOutput
	if mv.IsStreaming {
		logger.Infof("%s event step=%d agent=%s role=%s streaming=true tool=%s", tracePrefix(st), index, event.AgentName, mv.Role, mv.ToolName)
		return
	}
	role, contentLen, toolCalls, toolName := traceMessageSummary(mv.Message)
	if role == "" {
		role = mv.Role
	}
	if toolName == "" {
		toolName = mv.ToolName
	}
	logger.Infof("%s event step=%d agent=%s role=%s content_len=%d tool=%s tool_calls=%v", tracePrefix(st), index, event.AgentName, role, contentLen, toolName, toolCalls)
}

func logTraceFailure(ctx context.Context, err error, events []*adk.AgentEvent) {
	st := traceFromContext(ctx)
	if st == nil {
		return
	}
	logger.Infof("%s failed err=%v events=%d", tracePrefix(st), err, len(events))
	start := len(events) - 3
	if start < 0 {
		start = 0
	}
	for i := start; i < len(events); i++ {
		logTraceEvent(ctx, i+1, events[i])
	}
}
