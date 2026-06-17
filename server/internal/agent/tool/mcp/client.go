package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
)

type Client struct {
	url       string
	sessionID string
	mu        sync.Mutex
}

func NewClient(ctx context.Context, url string) (*Client, error) {
	c := &Client{url: url}
	sid, err := c.mcpInit(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp connect %s: %w", url, err)
	}
	c.sessionID = sid
	return c, nil
}

func (c *Client) ListTools(ctx context.Context) ([]tool.BaseTool, error) {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	bodyStr, err := c.mcpPost(ctx, payload, "Mcp-Session-Id", sessionID)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result *struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(bodyStr), &resp); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}
	if resp.Result == nil {
		return nil, fmt.Errorf("tools/list: no result")
	}

	var tools []tool.BaseTool
	usedNames := map[string]int{}
	for _, raw := range resp.Result.Tools {
		name, _ := raw["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := raw["description"].(string)
		if desc == "" {
			desc = name
		}

		var paramsOneOf *schema.ParamsOneOf
		if inputSchema, ok := raw["inputSchema"]; ok && inputSchema != nil {
			schemaBytes, err := json.Marshal(inputSchema)
			if err == nil {
				var js einojsonschema.Schema
				if err := json.Unmarshal(schemaBytes, &js); err == nil {
					paramsOneOf = schema.NewParamsOneOfByJSONSchema(&js)
				}
			}
		}

		tools = append(tools, &Tool{
			info: &schema.ToolInfo{
				Name:        UniqueSafeToolName(name, usedNames),
				Desc:        desc,
				ParamsOneOf: paramsOneOf,
			},
			client:   c,
			ToolName: name,
		})
	}
	return tools, nil
}

func (c *Client) Call(ctx context.Context, toolName string, args map[string]any) (string, error) {
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()

	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	})
	bodyStr, err := c.mcpPost(ctx, payload, "Mcp-Session-Id", sessionID)
	if err != nil {
		if sid, reErr := c.reinit(ctx); reErr == nil {
			c.mu.Lock()
			c.sessionID = sid
			c.mu.Unlock()
			return c.Call(ctx, toolName, args)
		}
		return fmt.Sprintf(`{"error":"tool call failed: %v"}`, err), nil
	}

	var mcpResp struct {
		Result *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(bodyStr), &mcpResp); err != nil {
		return fmt.Sprintf(`{"error":"parse response: %v"}`, err), nil
	}
	if mcpResp.Error != nil {
		return fmt.Sprintf(`{"error":"MCP error code=%d: %s"}`, mcpResp.Error.Code, mcpResp.Error.Message), nil
	}
	if mcpResp.Result == nil || len(mcpResp.Result.Content) == 0 {
		return `{"error":"empty result"}`, nil
	}
	return mcpResp.Result.Content[0].Text, nil
}

func (c *Client) mcpInit(ctx context.Context) (string, error) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"xiaoli-server","version":"1.0"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("MCP init: no session ID, body: %s", string(raw))
	}
	return sessionID, nil
}

func (c *Client) reinit(ctx context.Context) (string, error) {
	sid, err := c.mcpInit(ctx)
	if err != nil {
		return "", err
	}
	return sid, nil
}

func (c *Client) mcpPost(ctx context.Context, payload []byte, extraHeaders ...string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for i := 0; i+1 < len(extraHeaders); i += 2 {
		req.Header.Set(extraHeaders[i], extraHeaders[i+1])
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	bodyStr := string(raw)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("MCP HTTP %d: %s", resp.StatusCode, strings.TrimSpace(bodyStr))
	}
	return ExtractData(bodyStr), nil
}

func ExtractData(body string) string {
	lines := strings.Split(body, "\n")
	var data []string
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(data) > 0 {
		return strings.Join(data, "\n")
	}
	return strings.TrimSpace(body)
}
