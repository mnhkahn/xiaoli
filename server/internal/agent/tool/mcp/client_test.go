package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientListToolsUsesSessionID(t *testing.T) {
	var sawToolsListSession bool
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			_, _ = w.Write([]byte(`event: message
data: {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"test","version":"1"}}}

`))
		case "tools/list":
			sawToolsListSession = r.Header.Get("Mcp-Session-Id") == "session-1"
			if !sawToolsListSession {
				http.Error(w, `{"jsonrpc":"2.0","error":{"code":-32000,"message":"Server not initialized"},"id":null}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`event: message
data: {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"curl","description":"Fetch URL","inputSchema":{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}}]}}

`))
		default:
			t.Fatalf("unexpected MCP method %q", req.Method)
		}
	}))
	defer mcpServer.Close()

	client, err := NewClient(context.Background(), mcpServer.URL, "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if !sawToolsListSession {
		t.Fatal("tools/list did not send Mcp-Session-Id")
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Name != "curl" {
		t.Fatalf("tool name = %q, want curl", info.Name)
	}
}

func TestClientToolsExposeSafeNamesAndCallOriginalName(t *testing.T) {
	var calledToolName string
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Mcp-Session-Id", "session-1")
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte(`event: message
data: {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"test","version":"1"}}}

`))
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "session-1" {
				http.Error(w, `{"error":"missing session"}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`event: message
data: {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"weather.get","description":"Weather","inputSchema":{"type":"object","properties":{"city":{"type":"string"}}}}]}}

`))
		case "tools/call":
			calledToolName = req.Params.Name
			_, _ = w.Write([]byte(`event: message
data: {"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{\"ok\":true}"}]}}

`))
		default:
			t.Fatalf("unexpected MCP method %q", req.Method)
		}
	}))
	defer mcpServer.Close()

	client, err := NewClient(context.Background(), mcpServer.URL, "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	wrapped, ok := tools[0].(*Tool)
	if !ok {
		t.Fatalf("tool type = %T, want *Tool", tools[0])
	}
	info, err := wrapped.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Name != "weather_get" {
		t.Fatalf("exposed tool name = %q, want weather_get", info.Name)
	}
	if wrapped.ToolName != "weather.get" {
		t.Fatalf("wrapped original tool name = %q, want weather.get", wrapped.ToolName)
	}
	if _, err := wrapped.InvokableRun(context.Background(), `{"city":"北京"}`); err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if calledToolName != "weather.get" {
		t.Fatalf("called MCP tool name = %q, want weather.get", calledToolName)
	}
}
