package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/xiaoli/internal/agent/localapp"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
	agentevent "github.com/mnhkahn/xiaoli/internal/event"
)

// ACP v1 uses newline-delimited JSON-RPC over stdio. All responses, including
// permission responses from the client, are read by one loop; model runs must
// never block that loop. stdout is exclusively reserved for protocol messages.
type acpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *acpError       `json:"error,omitempty"`
}

type acpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *acpError) Error() string { return e.Message }

type acpSessionParams struct {
	SessionID  string            `json:"sessionId"`
	Cwd        string            `json:"cwd"`
	MCPServers []json.RawMessage `json:"mcpServers"`
}

type acpContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	URI      string `json:"uri"`
	Name     string `json:"name"`
	Resource *struct {
		URI  string  `json:"uri"`
		Text *string `json:"text"`
	} `json:"resource"`
}

type acpEmit func(map[string]any) error
type acpPermission func(*agentbuiltin.PendingToolUseConfirm) (bool, error)

// Keep the protocol independent from model/network access so the complete
// stdio exchange can be exercised with a deterministic backend.
type acpBackend interface {
	NewSession(context.Context, string) (string, error)
	LoadSession(context.Context, string, string) ([]*schema.Message, error)
	Prompt(context.Context, string, string, string, acpEmit, acpPermission) error
}

type acpServer struct {
	backend       func() (acpBackend, error)
	agent         acpBackend
	out           io.Writer
	writeMu       sync.Mutex
	mu            sync.Mutex
	sessions      map[string]string
	pending       map[string]chan acpMessage
	sequence      uint64
	initialized   bool
	activeSession string
	activeCancel  context.CancelFunc
	workers       sync.WaitGroup
}

func newACPServer(out io.Writer, backend func() (acpBackend, error)) *acpServer {
	return &acpServer{out: out, backend: backend, sessions: make(map[string]string), pending: make(map[string]chan acpMessage)}
}

func runACPCLI(args []string, in io.Reader, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("xiaoli acp", flag.ContinueOnError)
	flags.SetOutput(errOut)
	config := flags.String("config", "", "path to local xiaoli settings.json")
	prompt := flags.String("prompt", "", "extra system prompt appended after AGENT.md/SOUL.md")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errOut, "usage: xiaoli acp [-config path] [-prompt text]")
		return 2
	}
	configureTUILogger(*config)
	var app *localapp.App
	defer func() {
		if app != nil {
			app.Close()
		}
	}()
	server := newACPServer(out, func() (acpBackend, error) {
		var err error
		app, err = localapp.New(localapp.Options{ConfigPath: *config, Prompt: *prompt})
		if err != nil {
			return nil, fmt.Errorf("initialize Xiaoli: %w; run xiaoli -init and configure a model first", err)
		}
		return &acpLocalBackend{app: app}, nil
	})
	if err := server.serve(context.Background(), in); err != nil {
		fmt.Fprintln(errOut, "xiaoli acp:", err)
		return 1
	}
	return 0
}

func (s *acpServer) send(message any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return json.NewEncoder(s.out).Encode(message)
}

func (s *acpServer) reply(id json.RawMessage, result any, err error) error {
	if len(id) == 0 {
		return nil
	}
	message := map[string]any{"jsonrpc": "2.0", "id": id}
	if err != nil {
		failure := &acpError{Code: -32603, Message: err.Error()}
		errors.As(err, &failure)
		message["error"] = failure
	} else {
		message["result"] = result
	}
	return s.send(message)
}

func (s *acpServer) update(id string, update map[string]any) error {
	return s.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": id, "update": update}})
}

func (s *acpServer) serve(parent context.Context, in io.Reader) error {
	ctx, cancel := context.WithCancel(parent)
	defer func() { cancel(); s.workers.Wait() }()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	for scanner.Scan() {
		var message acpMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			if err := s.reply(json.RawMessage("null"), nil, &acpError{-32700, "Parse error"}); err != nil {
				return err
			}
			continue
		}
		if message.JSONRPC != "2.0" || !acpValidID(message.ID) {
			if err := s.reply(json.RawMessage("null"), nil, &acpError{-32600, "Invalid JSON-RPC request"}); err != nil {
				return err
			}
			continue
		}
		if message.Method == "" {
			s.mu.Lock()
			response := s.pending[string(message.ID)]
			s.mu.Unlock()
			if response != nil {
				select {
				case response <- message:
				default:
				}
			}
			continue
		}
		result, err := s.handle(ctx, message)
		if errors.Is(err, errACPPromptRunning) {
			continue
		}
		if err := s.reply(message.ID, result, err); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func acpValidID(id json.RawMessage) bool {
	if len(id) == 0 || string(id) == "null" {
		return true
	}
	var value any
	if json.Unmarshal(id, &value) != nil {
		return false
	}
	switch value.(type) {
	case string, float64:
		return true
	}
	return false
}

var errACPPromptRunning = errors.New("ACP prompt response is asynchronous")

func acpDecode(params json.RawMessage, value any) error {
	if len(params) == 0 || string(params) == "null" || params[0] != '{' {
		return &acpError{-32602, "Expected an object in params"}
	}
	if err := json.Unmarshal(params, value); err != nil {
		return &acpError{-32602, err.Error()}
	}
	return nil
}

func (s *acpServer) handle(ctx context.Context, message acpMessage) (any, error) {
	if message.Method == "session/cancel" {
		var params acpSessionParams
		if err := acpDecode(message.Params, &params); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.activeSession == params.SessionID && s.activeCancel != nil {
			s.activeCancel()
		}
		s.mu.Unlock()
		return nil, nil
	}
	if len(message.ID) == 0 {
		return nil, nil
	}
	if message.Method == "initialize" {
		var params struct {
			ProtocolVersion int `json:"protocolVersion"`
		}
		if err := acpDecode(message.Params, &params); err != nil {
			return nil, err
		}
		if params.ProtocolVersion <= 0 {
			return nil, &acpError{-32602, "protocolVersion must be a positive integer"}
		}
		s.initialized = true
		return map[string]any{
			"protocolVersion":   1,
			"agentInfo":         map[string]any{"name": "xiaoli", "title": "小李", "version": buildVersion()},
			"agentCapabilities": map[string]any{"loadSession": true, "promptCapabilities": map[string]any{"embeddedContext": true}},
			"authMethods":       []any{},
		}, nil
	}
	if !s.initialized {
		return nil, &acpError{-32600, "Call initialize before using sessions"}
	}
	switch message.Method {
	case "session/new", "session/load":
		var params acpSessionParams
		if err := acpDecode(message.Params, &params); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(params.Cwd) {
			return nil, &acpError{-32602, "cwd must be an absolute directory path"}
		}
		info, err := os.Stat(params.Cwd)
		if err != nil || !info.IsDir() {
			return nil, &acpError{-32602, "cwd must be an existing directory"}
		}
		params.Cwd = filepath.Clean(params.Cwd)
		if len(params.MCPServers) > 0 {
			return nil, &acpError{-32602, "Forwarded MCP servers are not supported yet; configure MCP in Xiaoli settings.json"}
		}
		s.mu.Lock()
		busy := s.activeCancel != nil
		s.mu.Unlock()
		if busy {
			return nil, &acpError{-32000, "Another prompt is running; cancel it or wait for completion"}
		}
		if s.agent == nil {
			s.agent, err = s.backend()
			if err != nil {
				return nil, err
			}
		}
		id := params.SessionID
		if message.Method == "session/new" {
			id, err = s.agent.NewSession(ctx, params.Cwd)
		} else {
			if id == "" {
				return nil, &acpError{-32602, "sessionId is required"}
			}
			var history []*schema.Message
			history, err = s.agent.LoadSession(ctx, id, params.Cwd)
			if err == nil {
				for _, item := range history {
					if item == nil || item.Content == "" {
						continue
					}
					kind := ""
					if item.Role == schema.User {
						kind = "user_message_chunk"
					}
					if item.Role == schema.Assistant {
						kind = "agent_message_chunk"
					}
					if kind != "" {
						if err = s.update(id, acpTextUpdate(kind, item.Content)); err != nil {
							break
						}
					}
				}
			}
		}
		if err != nil {
			return nil, err
		}
		s.sessions[id] = params.Cwd
		if message.Method == "session/load" {
			return map[string]any{}, nil
		}
		return map[string]any{"sessionId": id}, nil
	case "session/prompt":
		var params struct {
			SessionID string       `json:"sessionId"`
			Prompt    []acpContent `json:"prompt"`
		}
		if err := acpDecode(message.Params, &params); err != nil {
			return nil, err
		}
		cwd, ok := s.sessions[params.SessionID]
		if !ok {
			return nil, &acpError{-32602, "Unknown sessionId; create or load the session first"}
		}
		text, err := acpPromptText(params.Prompt)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.activeCancel != nil {
			s.mu.Unlock()
			return nil, &acpError{-32000, "Another prompt is running; cancel it or wait for completion"}
		}
		runCtx, cancel := context.WithTimeout(ctx, defaultChatTimeout)
		s.activeSession, s.activeCancel = params.SessionID, cancel
		s.mu.Unlock()
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			defer cancel()
			emit := func(update map[string]any) error { return s.update(params.SessionID, update) }
			permission := func(confirm *agentbuiltin.PendingToolUseConfirm) (bool, error) {
				return s.requestPermission(runCtx, params.SessionID, confirm)
			}
			err := s.agent.Prompt(runCtx, params.SessionID, cwd, text, emit, permission)
			result := map[string]any{"stopReason": "end_turn"}
			if errors.Is(runCtx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
				result["stopReason"], err = "cancelled", nil
			} else if runCtx.Err() != nil {
				err = runCtx.Err()
			}
			// Release the workspace before publishing completion so a client can
			// immediately issue its next prompt after reading the response.
			s.mu.Lock()
			s.activeSession, s.activeCancel = "", nil
			s.mu.Unlock()
			_ = s.reply(message.ID, result, err)
		}()
		return nil, errACPPromptRunning
	default:
		return nil, &acpError{-32601, "Method not found: " + message.Method}
	}
}

func acpTextUpdate(kind, text string) map[string]any {
	return map[string]any{"sessionUpdate": kind, "content": map[string]any{"type": "text", "text": text}}
}

func acpPromptText(blocks []acpContent) (string, error) {
	var parts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "resource_link":
			if block.URI == "" {
				return "", &acpError{-32602, "resource_link requires uri"}
			}
			parts = append(parts, fmt.Sprintf("Referenced resource: %s (%s)", block.URI, block.Name))
		case "resource":
			if block.Resource == nil || block.Resource.URI == "" || block.Resource.Text == nil {
				return "", &acpError{-32602, "resource requires a URI and text content"}
			}
			parts = append(parts, fmt.Sprintf("Resource %s:\n%s", block.Resource.URI, *block.Resource.Text))
		default:
			return "", &acpError{-32602, "Unsupported prompt content type: " + block.Type}
		}
	}
	text := strings.Join(parts, "\n\n")
	if strings.TrimSpace(text) == "" {
		return "", &acpError{-32602, "prompt must contain text or a resource"}
	}
	return text, nil
}

func (s *acpServer) requestPermission(ctx context.Context, sessionID string, confirm *agentbuiltin.PendingToolUseConfirm) (bool, error) {
	s.mu.Lock()
	s.sequence++
	id, _ := json.Marshal(fmt.Sprintf("xiaoli-permission-%d", s.sequence))
	response := make(chan acpMessage, 1)
	s.pending[string(id)] = response
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, string(id)); s.mu.Unlock() }()
	toolID := firstNonEmpty(confirm.ToolUseID, confirm.RequestID, string(id))
	err := s.send(map[string]any{
		"jsonrpc": "2.0", "id": json.RawMessage(id), "method": "session/request_permission",
		"params": map[string]any{
			"sessionId": sessionID,
			"toolCall":  map[string]any{"toolCallId": toolID, "title": confirm.Question, "kind": "execute", "status": "pending", "rawInput": map[string]any{"command": confirm.BashCommand}},
			"options": []any{
				map[string]any{"optionId": "allow-once", "name": "允许一次", "kind": "allow_once"},
				map[string]any{"optionId": "reject-once", "name": "拒绝", "kind": "reject_once"},
			},
		},
	})
	if err != nil {
		return false, err
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case message := <-response:
		if message.Error != nil {
			return false, message.Error
		}
		var result struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		if err := json.Unmarshal(message.Result, &result); err != nil {
			return false, err
		}
		if result.Outcome.Outcome == "cancelled" {
			return false, context.Canceled
		}
		if result.Outcome.Outcome != "selected" {
			return false, fmt.Errorf("invalid ACP permission outcome")
		}
		switch result.Outcome.OptionID {
		case "allow-once":
			return true, nil
		case "reject-once":
			return false, nil
		default:
			return false, fmt.Errorf("unknown ACP permission option")
		}
	}
}

type acpLocalBackend struct{ app *localapp.App }

func (b *acpLocalBackend) NewSession(ctx context.Context, cwd string) (string, error) {
	return b.app.Agent.NewSession(ctx, "acp", cwd)
}

func (b *acpLocalBackend) LoadSession(ctx context.Context, id, cwd string) ([]*schema.Message, error) {
	store := b.app.Agent.SessionManager()
	info, err := store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if info.ChannelName != "acp" || info.ChannelUser != cwd {
		return nil, &acpError{-32602, "Session belongs to a different channel or workspace"}
	}
	// Local session metadata does not contain messages; the conversation is
	// stored separately by the Agent's memory backend, just as in the TUI.
	raw, err := b.app.Agent.MemoryReader().LoadRaw(ctx, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw.Raw) == 0 {
		return nil, nil
	}
	var history []*schema.Message
	if err := json.Unmarshal(raw.Raw, &history); err != nil {
		return nil, err
	}
	return history, nil
}

func (b *acpLocalBackend) Prompt(ctx context.Context, id, cwd, text string, emit acpEmit, permission acpPermission) error {
	previous, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(cwd); err != nil {
		return err
	}
	defer os.Chdir(previous)
	b.app.Agent.SetAgentFileRoots([]string{cwd})
	defer b.app.Agent.SetAgentFileRoots(nil)
	defer agentbuiltin.ClearBashApproval(id)
	defer func() {
		for b.app.Agent.ConsumeToolUseConfirm(id) != nil {
		}
		b.app.Agent.ConsumeAskData(id)
	}()
	tracker := &acpToolTracker{emit: emit, calls: make(map[string][]string)}
	flushID := fmt.Sprintf("xiaoli-acp-flush-%d", acpToolSequence.Add(1))
	flushed := make(chan struct{})
	unsubscribe := b.app.Bus.SubscribeAll(func(_ context.Context, event agentevent.Event) error {
		if event.ID == flushID {
			close(flushed)
			return nil
		}
		if event.SessionID != id || ctx.Err() != nil {
			return nil
		}
		return tracker.event(event)
	})
	defer func() {
		// The bus delivers asynchronously. Drain queued tool updates before
		// publishing completion, then prevent any late event from leaking into
		// the next prompt. Cancellation does not wait for the drain.
		_ = b.app.Bus.Publish(context.Background(), agentevent.Event{ID: flushID, Type: "acp.flush"})
		select {
		case <-flushed:
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
		tracker.mu.Lock()
		tracker.closed = true
		tracker.mu.Unlock()
		unsubscribe()
	}()
	for turn := 0; turn < 32; turn++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var streamErr error
		streamed := false
		reply, err := b.app.Agent.ChatWithContextOptions(ctx, id, cwd, text, agentruntime.ChatOptions{
			Channel: "acp", SessionID: id,
			// These tools require the TUI commit panel or background workspace
			// ownership. ACP currently executes one foreground turn at a time.
			DisabledTools: []string{"commit", "task"},
			Stream: func(delta string) bool {
				if delta == "" {
					return ctx.Err() == nil
				}
				streamed = true
				streamErr = emit(acpTextUpdate("agent_message_chunk", delta))
				return streamErr == nil && ctx.Err() == nil
			},
		})
		if streamErr != nil {
			return streamErr
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		confirm := b.app.Agent.ConsumeToolUseConfirm(id)
		if confirm == nil {
			if !streamed && reply != "" {
				if err := emit(acpTextUpdate("agent_message_chunk", reply)); err != nil {
					return err
				}
			}
			if ask := b.app.Agent.ConsumeAskData(id); ask != nil && len(ask.Options) > 0 {
				if err := emit(acpTextUpdate("agent_message_chunk", "\n\n"+ask.Question+"\n"+strings.Join(ask.Options, " / "))); err != nil {
					return err
				}
			}
			return nil
		}
		if confirm.ToolName != "bash" {
			return fmt.Errorf("ACP cannot resume permission for tool %s", confirm.ToolName)
		}
		text, err = acpResumeBash(ctx, id, cwd, confirm, emit, permission)
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("ACP permission continuation limit reached")
}

func acpResumeBash(ctx context.Context, id, cwd string, confirm *agentbuiltin.PendingToolUseConfirm, emit acpEmit, permission acpPermission) (string, error) {
	toolID := firstNonEmpty(confirm.ToolUseID, confirm.RequestID)
	if err := emit(map[string]any{"sessionUpdate": "tool_call", "toolCallId": toolID, "title": confirm.Question, "kind": "execute", "status": "pending"}); err != nil {
		return "", err
	}
	allowed, err := permission(confirm)
	if err != nil {
		return "", err
	}
	// Recheck cancellation even when an allow response races with cancel.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	command, _, expired, ok := agentbuiltin.PendingBashApproval(id, confirm.BashHash)
	agentbuiltin.ClearBashApprovalHash(id, confirm.BashHash)
	if !allowed {
		if err := emit(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": toolID, "status": "failed"}); err != nil {
			return "", err
		}
		return fmt.Sprintf("[TOOL_RESULT]\ntool=bash\ntool_use_id=%s\nstatus=rejected\n用户拒绝执行命令。不得执行或重试该命令。\n[/TOOL_RESULT]\n请基于用户拒绝的结果回应。", toolID), nil
	}
	if !ok || expired {
		if err := emit(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": toolID, "status": "failed"}); err != nil {
			return "", err
		}
		return formatExpiredBashFollowup(toolID, confirm.BashCommand), nil
	}
	if err := emit(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": toolID, "status": "in_progress"}); err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	output, runErr := runShellCommandContext(runCtx, cwd, command)
	cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	status := "completed"
	if normalizeApprovedBashError(output, runErr) != nil {
		status = "failed"
	}
	if err := emit(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": toolID, "status": status, "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": output}}}}); err != nil {
		return "", err
	}
	return formatApprovedBashFollowup(toolID, command, output, runErr), nil
}

// Runtime tool events have no call ID. Match repeated calls by name/input and
// give each occurrence a distinct ID for the editor's tool activity display.
type acpToolTracker struct {
	mu     sync.Mutex
	emit   acpEmit
	calls  map[string][]string
	closed bool
}

var acpToolSequence atomic.Uint64

func (t *acpToolTracker) event(event agentevent.Event) error {
	if event.Type != agentevent.TypeAgentToolStarted && event.Type != agentevent.TypeAgentToolFinished {
		return nil
	}
	data, ok := event.Data.(map[string]any)
	if !ok {
		return nil
	}
	name, _ := data["name"].(string)
	args, _ := data["arguments"].(string)
	// Bash interrupts are handled explicitly with their actual approval IDs.
	// Commands allowed by Xiaoli's existing policy still need an activity card.
	if name == "bash" {
		if event.Type != agentevent.TypeAgentToolFinished || data["error"] != nil {
			return nil
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.closed {
			return nil
		}
		id := fmt.Sprintf("xiaoli-tool-%d", acpToolSequence.Add(1))
		if err := t.emit(map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": "bash", "kind": "execute", "status": "in_progress", "rawInput": args}); err != nil {
			return err
		}
		return t.emit(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": "completed", "rawOutput": data})
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	key := name + "\x00" + args
	if event.Type == agentevent.TypeAgentToolStarted {
		id := fmt.Sprintf("xiaoli-tool-%d", acpToolSequence.Add(1))
		t.calls[key] = append(t.calls[key], id)
		var input any
		if json.Unmarshal([]byte(args), &input) != nil {
			input = args
		}
		return t.emit(map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": name, "kind": acpToolKind(name), "status": "in_progress", "rawInput": input})
	}
	queue := t.calls[key]
	if len(queue) == 0 {
		return nil
	}
	id := queue[0]
	t.calls[key] = queue[1:]
	status := "completed"
	if data["error"] != nil {
		status = "failed"
	}
	return t.emit(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status, "rawOutput": data})
}

func acpToolKind(name string) string {
	switch {
	case strings.Contains(name, "write"), strings.Contains(name, "edit"):
		return "edit"
	case strings.Contains(name, "read"), strings.Contains(name, "list"):
		return "read"
	case strings.Contains(name, "search"):
		return "search"
	case strings.Contains(name, "fetch"):
		return "fetch"
	default:
		return "other"
	}
}
