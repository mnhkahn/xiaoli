package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
)

// 认证方式（与 runtime.MCPAuth* 对应）
const (
	authNone   = "none"
	authQuery  = "query"
	authBearer = "bearer"
	authHeader = "header"
	authOAuth  = "oauth"
)

// AuthConfig MCP 连接的认证配置
type AuthConfig struct {
	URL          string
	APIKey       string
	Auth         string
	HeaderName   string
	TokenURL     string
	ClientID     string
	ClientSecret string
	RefreshToken string
	Scope        string
	// Timeout covers one tools/call request and any wait for this client's call gate.
	Timeout time.Duration
}

const defaultCallTimeout = 120 * time.Second

type Client struct {
	url       string
	auth      AuthConfig
	sessionID string
	mu        sync.Mutex

	// OAuth access token 缓存
	oauthToken  string
	oauthExpiry time.Time
	oauthMu     sync.Mutex
	callGate    chan struct{}
	timeout     time.Duration
}

func NewClient(ctx context.Context, cfg AuthConfig) (*Client, error) {
	if cfg.Auth == "" {
		// 兼容旧逻辑：有 key 走 query，无 key 走 none
		if cfg.APIKey != "" {
			cfg.Auth = authQuery
		} else {
			cfg.Auth = authNone
		}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultCallTimeout
	}
	c := &Client{url: cfg.URL, auth: cfg, callGate: make(chan struct{}, 1), timeout: timeout}
	c.callGate <- struct{}{}
	sid, err := c.mcpInit(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp connect %s: %w", cfg.URL, err)
	}
	c.sessionID = sid
	return c, nil
}

func (c *Client) requestURL() string {
	if c.auth.Auth == authQuery && c.auth.APIKey != "" {
		if strings.ContainsRune(c.url, '?') {
			return c.url + "&key=" + url.QueryEscape(c.auth.APIKey)
		}
		return c.url + "?key=" + url.QueryEscape(c.auth.APIKey)
	}
	return c.url
}

// applyAuthHeaders 按认证方式设置请求头（query 模式不在这里处理）
func (c *Client) applyAuthHeaders(ctx context.Context, req *http.Request) error {
	switch c.auth.Auth {
	case authBearer:
		if c.auth.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.auth.APIKey)
		}
	case authHeader:
		name := c.auth.HeaderName
		if name == "" {
			name = "X-API-Key"
		}
		if c.auth.APIKey != "" {
			req.Header.Set(name, c.auth.APIKey)
		}
	case authOAuth:
		token, err := c.oauthAccessToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

// oauthAccessToken 返回有效的 access token，过期则用 refresh_token 刷新（带缓存）
func (c *Client) oauthAccessToken(ctx context.Context) (string, error) {
	c.oauthMu.Lock()
	defer c.oauthMu.Unlock()
	if c.oauthToken != "" && time.Now().Before(c.oauthExpiry) {
		return c.oauthToken, nil
	}
	form := url.Values{}
	if c.auth.RefreshToken != "" {
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", c.auth.RefreshToken)
	} else {
		form.Set("grant_type", "client_credentials")
	}
	form.Set("client_id", c.auth.ClientID)
	form.Set("client_secret", c.auth.ClientSecret)
	if c.auth.Scope != "" {
		form.Set("scope", c.auth.Scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.auth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("oauth token failed: %d %s", resp.StatusCode, string(raw))
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", fmt.Errorf("parse oauth token: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("oauth token empty: %s", string(raw))
	}
	// 提供商可能轮换 refresh_token，需更新缓存，否则下次刷新会用失效的旧 token。
	if tok.RefreshToken != "" {
		c.auth.RefreshToken = tok.RefreshToken
	}
	expire := tok.ExpiresIn
	if expire <= 0 {
		expire = 3600
	}
	// 提前刷新的安全余量：默认 300s，但对短期 token 钳制到有效期的一半，
	// 避免 expire<=300 时偏移为负导致缓存永远失效、每次请求都重取 token。
	skew := 300
	if skew > expire/2 {
		skew = expire / 2
	}
	c.oauthToken = tok.AccessToken
	c.oauthExpiry = time.Now().Add(time.Duration(expire-skew) * time.Second)
	return c.oauthToken, nil
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
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case <-callCtx.Done():
		return fmt.Sprintf(`{"error":"tool call failed: %v"}`, callCtx.Err()), nil
	case <-c.callGate:
	}
	defer func() { c.callGate <- struct{}{} }()
	return c.call(callCtx, toolName, args)
}

func (c *Client) call(ctx context.Context, toolName string, args map[string]any) (string, error) {
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
			bodyStr, err = c.mcpPost(ctx, payload, "Mcp-Session-Id", sid)
			if err == nil {
				return c.parseCallResponse(bodyStr)
			}
		}
		return fmt.Sprintf(`{"error":"tool call failed: %v"}`, err), nil
	}
	return c.parseCallResponse(bodyStr)
}

func (c *Client) parseCallResponse(bodyStr string) (string, error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.requestURL(), strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if err := c.applyAuthHeaders(ctx, req); err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		raw, _ := io.ReadAll(resp.Body)
		var initResp struct {
			Result *json.RawMessage `json:"result"`
		}
		if json.Unmarshal(raw, &initResp) == nil && initResp.Result != nil {
			return "", nil
		}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.requestURL(), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if err := c.applyAuthHeaders(ctx, req); err != nil {
		return "", err
	}
	for i := 0; i+1 < len(extraHeaders); i += 2 {
		if extraHeaders[i+1] != "" {
			req.Header.Set(extraHeaders[i], extraHeaders[i+1])
		}
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
