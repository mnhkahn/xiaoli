package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/xiaoli/internal/agent/localapp"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
	agentevent "github.com/mnhkahn/xiaoli/internal/event"
)

type acpFakeBackend struct {
	prompt func(context.Context, string, string, string, acpEmit, acpPermission) error
	load   func(context.Context, string, string) ([]*schema.Message, error)
}

func (b *acpFakeBackend) NewSession(context.Context, string) (string, error) { return "session-1", nil }
func (b *acpFakeBackend) LoadSession(ctx context.Context, id, cwd string) ([]*schema.Message, error) {
	if b.load != nil {
		return b.load(ctx, id, cwd)
	}
	return nil, nil
}
func (b *acpFakeBackend) Prompt(ctx context.Context, id, cwd, text string, emit acpEmit, permission acpPermission) error {
	return b.prompt(ctx, id, cwd, text, emit, permission)
}

type acpTestClient struct {
	t        *testing.T
	in       *io.PipeWriter
	messages chan acpMessage
}

func startACPTestClient(t *testing.T, backend acpBackend) *acpTestClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	client := &acpTestClient{t: t, in: inW, messages: make(chan acpMessage, 32)}
	done := make(chan error, 1)
	go func() {
		s := newACPServer(outW, func() (acpBackend, error) { return backend, nil })
		done <- s.serve(context.Background(), inR)
		outW.Close()
	}()
	go func() {
		defer close(client.messages)
		decoder := json.NewDecoder(outR)
		for {
			var message acpMessage
			if decoder.Decode(&message) != nil {
				return
			}
			client.messages <- message
		}
	}()
	t.Cleanup(func() {
		inW.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("ACP worker failed to stop after EOF")
		}
		outR.Close()
		inR.Close()
	})
	return client
}

func (c *acpTestClient) send(id any, method string, params any) {
	c.t.Helper()
	message := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id != nil {
		message["id"] = id
	}
	if err := json.NewEncoder(c.in).Encode(message); err != nil {
		c.t.Fatal(err)
	}
}

func (c *acpTestClient) next() acpMessage {
	c.t.Helper()
	select {
	case message, ok := <-c.messages:
		if !ok {
			c.t.Fatal("ACP output closed unexpectedly")
		}
		return message
	case <-time.After(3 * time.Second):
		c.t.Fatal("timed out waiting for ACP message")
	}
	return acpMessage{}
}

func (c *acpTestClient) session(cwd string) {
	c.t.Helper()
	c.send(1, "initialize", map[string]any{"protocolVersion": 1})
	message := c.next()
	if message.Error != nil || !strings.Contains(string(message.Result), `"protocolVersion":1`) {
		c.t.Fatalf("initialize: %+v", message)
	}
	c.send(2, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	message = c.next()
	if message.Error != nil || !strings.Contains(string(message.Result), `"sessionId":"session-1"`) {
		c.t.Fatalf("session/new: %+v", message)
	}
}

func acpTestPrompt(c *acpTestClient, id int) {
	c.send(id, "session/prompt", map[string]any{"sessionId": "session-1", "prompt": []any{map[string]any{"type": "text", "text": "hello"}}})
}

func TestACPStreamAndWorkspace(t *testing.T) {
	cwd := t.TempDir()
	backend := &acpFakeBackend{prompt: func(_ context.Context, id, dir, text string, emit acpEmit, _ acpPermission) error {
		if id != "session-1" || dir != cwd || text != "hello" {
			return fmt.Errorf("wrong prompt context: %s %s %s", id, dir, text)
		}
		if err := emit(acpTextUpdate("agent_message_chunk", "one")); err != nil {
			return err
		}
		return emit(acpTextUpdate("agent_message_chunk", "two"))
	}}
	client := startACPTestClient(t, backend)
	client.session(cwd)
	acpTestPrompt(client, 3)
	for _, want := range []string{"one", "two"} {
		message := client.next()
		if message.Method != "session/update" || !strings.Contains(string(message.Params), `"text":"`+want+`"`) {
			t.Fatalf("stream: %+v", message)
		}
	}
	message := client.next()
	if string(message.ID) != "3" || string(message.Result) != `{"stopReason":"end_turn"}` {
		t.Fatalf("completion: %+v", message)
	}
	acpTestPrompt(client, 4)
	client.next()
	client.next()
	if string(client.next().ID) != "4" {
		t.Fatal("next prompt was not accepted after completion")
	}
}

func TestACPPermissionAllowRejectAndCancel(t *testing.T) {
	for _, choice := range []string{"allow-once", "reject-once", "cancelled", "session-cancel"} {
		t.Run(choice, func(t *testing.T) {
			backend := &acpFakeBackend{prompt: func(ctx context.Context, _, _, _ string, emit acpEmit, permission acpPermission) error {
				allowed, err := permission(&agentbuiltin.PendingToolUseConfirm{ToolName: "bash", ToolUseID: "call-1", BashCommand: "echo ok", Question: "Run echo?"})
				if err != nil {
					return err
				}
				return emit(acpTextUpdate("agent_message_chunk", fmt.Sprintf("allowed=%v", allowed)))
			}}
			client := startACPTestClient(t, backend)
			client.session(t.TempDir())
			acpTestPrompt(client, 3)
			request := client.next()
			if request.Method != "session/request_permission" {
				t.Fatalf("permission: %+v", request)
			}
			if choice == "session-cancel" {
				client.send(nil, "session/cancel", map[string]any{"sessionId": "session-1"})
			} else {
				outcome := map[string]any{"outcome": "selected", "optionId": choice}
				if choice == "cancelled" {
					outcome = map[string]any{"outcome": "cancelled"}
				}
				if err := json.NewEncoder(client.in).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"outcome": outcome}}); err != nil {
					t.Fatal(err)
				}
			}
			if choice == "allow-once" || choice == "reject-once" {
				message := client.next()
				if !strings.Contains(string(message.Params), fmt.Sprintf("allowed=%v", choice == "allow-once")) {
					t.Fatalf("decision: %+v", message)
				}
			}
			message := client.next()
			stop := "end_turn"
			if choice == "cancelled" || choice == "session-cancel" {
				stop = "cancelled"
			}
			if message.Error != nil || string(message.ID) != "3" || !strings.Contains(string(message.Result), stop) {
				t.Fatalf("completion: %+v", message)
			}
		})
	}
}

func TestACPBusyAndCancellation(t *testing.T) {
	started := make(chan struct{})
	backend := &acpFakeBackend{prompt: func(ctx context.Context, _, _, _ string, _ acpEmit, _ acpPermission) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	client := startACPTestClient(t, backend)
	client.session(t.TempDir())
	acpTestPrompt(client, 3)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("prompt did not start")
	}
	acpTestPrompt(client, 4)
	message := client.next()
	if string(message.ID) != "4" || message.Error == nil || message.Error.Code != -32000 {
		t.Fatalf("busy: %+v", message)
	}
	// Cancelling a different session must not interrupt this prompt.
	client.send(nil, "session/cancel", map[string]any{"sessionId": "other"})
	client.send(5, "initialize", map[string]any{"protocolVersion": 1})
	if string(client.next().ID) != "5" {
		t.Fatal("wrong session cancellation interrupted prompt")
	}
	client.send(nil, "session/cancel", map[string]any{"sessionId": "session-1"})
	message = client.next()
	if string(message.ID) != "3" || !strings.Contains(string(message.Result), "cancelled") {
		t.Fatalf("cancel: %+v", message)
	}
}

func TestACPReplayAndValidation(t *testing.T) {
	backend := &acpFakeBackend{load: func(_ context.Context, id, cwd string) ([]*schema.Message, error) {
		return []*schema.Message{schema.SystemMessage("secret"), schema.UserMessage("question"), schema.AssistantMessage("answer", nil)}, nil
	}}
	client := startACPTestClient(t, backend)
	client.send(1, "session/new", map[string]any{"cwd": t.TempDir()})
	if e := client.next().Error; e == nil || e.Code != -32600 {
		t.Fatalf("initialization guard: %v", e)
	}
	client.session(t.TempDir())
	client.send(3, "session/load", map[string]any{"sessionId": "saved", "cwd": t.TempDir(), "mcpServers": []any{}})
	for _, want := range []string{"user_message_chunk", "agent_message_chunk"} {
		message := client.next()
		if message.Method != "session/update" || !strings.Contains(string(message.Params), want) || strings.Contains(string(message.Params), "secret") {
			t.Fatalf("replay: %+v", message)
		}
	}
	if string(client.next().Result) != "{}" {
		t.Fatal("missing load result")
	}
	client.send(4, "session/new", map[string]any{"cwd": "relative"})
	if e := client.next().Error; e == nil || e.Code != -32602 {
		t.Fatalf("relative cwd: %v", e)
	}
	client.send(5, "session/prompt", map[string]any{"sessionId": "missing", "prompt": []any{}})
	if e := client.next().Error; e == nil || e.Code != -32602 {
		t.Fatalf("unknown session: %v", e)
	}
	client.send(6, "unsupported", map[string]any{})
	if e := client.next().Error; e == nil || e.Code != -32601 {
		t.Fatalf("unknown method: %v", e)
	}
	if _, err := client.in.Write([]byte("invalid json\n")); err != nil {
		t.Fatal(err)
	}
	if e := client.next().Error; e == nil || e.Code != -32700 {
		t.Fatalf("parse error: %v", e)
	}
	client.send(7, "initialize", map[string]any{"protocolVersion": 2})
	if !strings.Contains(string(client.next().Result), `"protocolVersion":1`) {
		t.Fatal("missing version negotiation")
	}
}

func TestACPResources(t *testing.T) {
	var blocks []acpContent
	err := json.Unmarshal([]byte(`[{"type":"text","text":"inspect"},{"type":"resource_link","uri":"file:///tmp/a.go","name":"a.go"},{"type":"resource","resource":{"uri":"file:///tmp/b.go","text":"package main"}}]`), &blocks)
	if err != nil {
		t.Fatal(err)
	}
	text, err := acpPromptText(blocks)
	if err != nil || !strings.Contains(text, "file:///tmp/a.go") || !strings.Contains(text, "package main") {
		t.Fatalf("resources: %q, %v", text, err)
	}
	if _, err := acpPromptText([]acpContent{{Type: "image"}}); err == nil {
		t.Fatal("unsupported image silently accepted")
	}
	if _, err := acpPromptText(nil); err == nil {
		t.Fatal("empty prompt silently accepted")
	}
}

func TestACPApprovedBashResumesWithActualToolResult(t *testing.T) {
	for _, choice := range []string{"allow", "reject", "cancel", "stale"} {
		t.Run(choice, func(t *testing.T) {
			cwd := t.TempDir()
			id := "acp-bash-" + choice
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = context.WithValue(ctx, agentbuiltin.SubAgentParentKey, id)
			ctx, holder := agentbuiltin.NewToolUseConfirmHolder(ctx)
			tool := agentbuiltin.NewShellTool(agentbuiltin.ShellConfig{Enabled: true})
			_, _ = tool.InvokableRun(ctx, `{"command":"printf done > result.txt"}`)
			confirm := holder.Get()
			if confirm == nil {
				t.Fatal("shell did not create approval")
			}
			defer agentbuiltin.ClearBashApproval(id)
			var updates []map[string]any
			emit := func(update map[string]any) error { updates = append(updates, update); return nil }
			permission := func(*agentbuiltin.PendingToolUseConfirm) (bool, error) {
				if choice == "cancel" {
					cancel()
				}
				if choice == "stale" {
					agentbuiltin.ClearBashApproval(id)
				}
				return choice != "reject", nil
			}
			text, err := acpResumeBash(ctx, id, cwd, confirm, emit, permission)
			body, readErr := os.ReadFile(filepath.Join(cwd, "result.txt"))
			if choice == "allow" {
				if err != nil || string(body) != "done" || !strings.Contains(text, "status=success") || !strings.Contains(text, confirm.ToolUseID) {
					t.Fatalf("approved: %q, %q, %v", body, text, err)
				}
				if updates[len(updates)-1]["status"] != "completed" {
					t.Fatal("missing completed tool status")
				}
			} else {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("command ran after %s", choice)
				}
				if choice == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel: %v", err)
				}
				if choice == "reject" && !strings.Contains(text, "status=rejected") {
					t.Fatalf("reject: %q", text)
				}
				if choice == "stale" && !strings.Contains(text, "status=expired") {
					t.Fatalf("stale: %q", text)
				}
			}
		})
	}
}

func TestACPToolTrackingIDsAndFailure(t *testing.T) {
	var updates []map[string]any
	tracker := &acpToolTracker{calls: make(map[string][]string), emit: func(update map[string]any) error { updates = append(updates, update); return nil }}
	data := map[string]any{"name": "read_file", "arguments": `{"path":"a.go"}`}
	for i := 0; i < 2; i++ {
		if err := tracker.event(agentevent.Event{Type: agentevent.TypeAgentToolStarted, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	if updates[0]["toolCallId"] == updates[1]["toolCallId"] {
		t.Fatal("duplicate tool IDs")
	}
	data["error"] = "read failed"
	if err := tracker.event(agentevent.Event{Type: agentevent.TypeAgentToolFinished, Data: data}); err != nil {
		t.Fatal(err)
	}
	if updates[2]["toolCallId"] != updates[0]["toolCallId"] || updates[2]["status"] != "failed" {
		t.Fatalf("failed tool update: %+v", updates[2])
	}
}

func TestACPLocalBackendWithMockModel(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"mock\",\"object\":\"chat.completion.chunk\",\"model\":\"mock\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello from mock\"},\"finish_reason\":null}]}\n\n")
			fmt.Fprint(w, "data: {\"id\":\"mock\",\"object\":\"chat.completion.chunk\",\"model\":\"mock\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		contract := `{"goal":"greet","requirements":[{"capability":"conversation.reply","required":false}],"verification":"reply","risk":"read_only"}`
		json.NewEncoder(w).Encode(map[string]any{"id": "mock", "object": "chat.completion", "model": "mock", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": contract}, "finish_reason": "stop"}}})
	}))
	defer model.Close()
	bus := agentevent.NewBus()
	agent := agentruntime.NewAgent(agentruntime.Config{LLMURL: model.URL + "/v1/chat/completions", LLMAPIKey: "mock", LLMModel: "mock", StorageBackend: "local", LocalDataDir: t.TempDir(), LLMTimeout: 5 * time.Second}, bus)
	if agent == nil {
		t.Fatal("agent initialization failed")
	}
	backend := &acpLocalBackend{app: &localapp.App{Agent: agent, Bus: bus}}
	cwd := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := backend.NewSession(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	var reply strings.Builder
	err = backend.Prompt(ctx, id, cwd, "hello", func(update map[string]any) error {
		if update["sessionUpdate"] == "agent_message_chunk" {
			reply.WriteString(update["content"].(map[string]any)["text"].(string))
		}
		return nil
	}, func(*agentbuiltin.PendingToolUseConfirm) (bool, error) {
		return false, fmt.Errorf("unexpected permission")
	})
	if err != nil || reply.String() != "hello from mock" {
		t.Fatalf("local runtime stream: %q, %v", reply.String(), err)
	}
	current, err := os.Getwd()
	if err != nil || current != previous {
		t.Fatalf("workspace was not restored: %s, %v", current, err)
	}
	history, err := backend.LoadSession(ctx, id, cwd)
	if err != nil || len(history) < 2 {
		t.Fatalf("persisted session: %d messages, %v", len(history), err)
	}
	if _, err := backend.LoadSession(ctx, id, t.TempDir()); err == nil {
		t.Fatal("loaded session into another workspace")
	}
}
