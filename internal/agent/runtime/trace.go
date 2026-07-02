package runtime

import (
	"context"
	"encoding/json"
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
	actions  []string
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

func (s *traceState) rememberAction(action string) {
	if s == nil || strings.TrimSpace(action) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	const maxActions = 20
	s.actions = append(s.actions, action)
	if len(s.actions) > maxActions {
		s.actions = s.actions[len(s.actions)-maxActions:]
	}
}

func (s *traceState) recentActions(limit int) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.actions) {
		limit = len(s.actions)
	}
	start := len(s.actions) - limit
	out := make([]string, limit)
	copy(out, s.actions[start:])
	return out
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

func traceArgsSummary(raw string, maxLen int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return traceTruncate(raw, maxLen)
	}
	parts := make([]string, 0, 3)
	if skill, _ := obj["skill"].(string); skill != "" {
		parts = append(parts, "skill="+skill)
	}
	if cmd, _ := obj["cmd"].(string); cmd != "" {
		parts = append(parts, "cmd="+traceTruncate(cmd, maxLen))
	}
	if argv, ok := obj["argv"].([]any); ok && len(argv) > 0 {
		vals := make([]string, 0, len(argv))
		for _, item := range argv {
			vals = append(vals, fmt.Sprint(item))
		}
		parts = append(parts, "argv="+traceTruncate(strings.Join(vals, " "), maxLen))
	}
	if len(parts) == 0 {
		return traceTruncate(raw, maxLen)
	}
	return strings.Join(parts, " ")
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

func traceMessagesStats(msgs []*schema.Message) (chars int, nonSystem int) {
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		chars += len(msg.Content)
		if msg.Role != schema.System {
			nonSystem++
		}
	}
	return chars, nonSystem
}

func logTraceModelStart(ctx context.Context, run traceRun, msgs []*schema.Message, toolCount int) {
	st := traceFromContext(ctx)
	if st == nil {
		return
	}
	chars, nonSystem := traceMessagesStats(msgs)
	logger.Infof("%s model.start step=%d input_messages=%d non_system_messages=%d prompt_chars=%d tools=%d", tracePrefix(st), run.Step, len(msgs), nonSystem, chars, toolCount)
	st.rememberAction(fmt.Sprintf("model#%d start messages=%d prompt_chars=%d tools=%d", run.Step, len(msgs), chars, toolCount))
}

func logTraceModelEnd(ctx context.Context, run traceRun, msg *schema.Message, promptTokens, completionTokens, totalTokens int, elapsed time.Duration) {
	st := traceFromContext(ctx)
	if st == nil {
		return
	}
	role, contentLen, toolCalls, toolName := traceMessageSummary(msg)
	logger.Infof("%s model.end step=%d role=%s content_len=%d tool=%s tool_calls=%v tokens={prompt:%d completion:%d total:%d} elapsed=%v", tracePrefix(st), run.Step, role, contentLen, toolName, toolCalls, promptTokens, completionTokens, totalTokens, elapsed)
	if len(toolCalls) > 0 {
		st.rememberAction(fmt.Sprintf("model#%d tool_calls=%v content_len=%d", run.Step, toolCalls, contentLen))
		return
	}
	st.rememberAction(fmt.Sprintf("model#%d role=%s content_len=%d", run.Step, role, contentLen))
}

func logTraceModelError(ctx context.Context, run traceRun, name string, err error, elapsed time.Duration) {
	st := traceFromContext(ctx)
	if st == nil {
		return
	}
	logger.Infof("%s model.error step=%d model=%s elapsed=%v err=%v", tracePrefix(st), run.Step, name, elapsed, err)
	st.rememberAction(fmt.Sprintf("model#%d error=%v", run.Step, err))
}

func logTraceToolStart(ctx context.Context, step int, name, category, args string) {
	st := traceFromContext(ctx)
	if st == nil {
		return
	}
	argSummary := traceArgsSummary(args, st.Options.MaxValueLength)
	msg := fmt.Sprintf("%s tool.start tool_step=%d name=%s category=%s args_len=%d", tracePrefix(st), step, name, category, len(args))
	if argSummary != "" {
		msg += fmt.Sprintf(" args_summary=%q", argSummary)
	}
	if st.Options.LogInputs && args != "" {
		msg += fmt.Sprintf(" args=%q", traceTruncate(args, st.Options.MaxValueLength))
	}
	logger.Infof("%s", msg)
	st.rememberAction(fmt.Sprintf("tool#%d %s start %s", step, name, argSummary))
}

func logTraceToolEnd(ctx context.Context, step int, name, category string, outputLen int, elapsed time.Duration, err error, output string) {
	st := traceFromContext(ctx)
	if st == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	msg := fmt.Sprintf("%s tool.end tool_step=%d name=%s category=%s status=%s output_len=%d elapsed=%v", tracePrefix(st), step, name, category, status, outputLen, elapsed)
	if err != nil {
		msg += fmt.Sprintf(" err=%v", err)
	}
	if st.Options.LogOutputs && output != "" {
		msg += fmt.Sprintf(" output=%q", traceTruncate(output, st.Options.MaxValueLength))
	}
	logger.Infof("%s", msg)
	st.rememberAction(fmt.Sprintf("tool#%d %s %s output_len=%d elapsed=%v", step, name, status, outputLen, elapsed))
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
	logger.Infof("%s failed err=%v events=%d recent_actions=%q", tracePrefix(st), err, len(events), st.recentActions(10))
	start := len(events) - 3
	if start < 0 {
		start = 0
	}
	for i := start; i < len(events); i++ {
		logTraceEvent(ctx, i+1, events[i])
	}
}
