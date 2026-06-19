package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Client struct {
	appID      string
	appToken   string
	httpClient *http.Client
}

type ClientConfig struct {
	AppID      string
	AppToken   string
	HTTPClient *http.Client
}

func NewClient(cfg ClientConfig) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		appID:      cfg.AppID,
		appToken:   cfg.AppToken,
		httpClient: httpClient,
	}
}

func (c *Client) ReplyText(ctx context.Context, messageID string, text string) error {
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	return c.reply(ctx, messageID, "text", string(content))
}

func (c *Client) ReplyPost(ctx context.Context, messageID string, markdown string) error {
	content, err := json.Marshal(markdownToPostContent(markdown))
	if err != nil {
		return err
	}
	return c.reply(ctx, messageID, "post", string(content))
}

func (c *Client) reply(ctx context.Context, messageID string, msgType string, content string) error {
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"msg_type": msgType,
		"content":  content,
	})
	if err != nil {
		return err
	}
	endpoint := "https://open.larksuite.com/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/reply"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("lark reply failed: %d %s", resp.StatusCode, string(raw))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if code, _ := int64Value(payload["code"]); code != 0 {
		return fmt.Errorf("lark reply failed: %v", payload)
	}
	return nil
}

func markdownToPostContent(markdown string) map[string]any {
	lines := strings.Split(strings.TrimSpace(markdown), "\n")
	var content [][]map[string]string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		text := line
		if strings.HasPrefix(text, "### ") {
			text = strings.TrimSpace(strings.TrimPrefix(text, "### "))
		} else if strings.HasPrefix(text, "## ") {
			text = strings.TrimSpace(strings.TrimPrefix(text, "## "))
		} else if strings.HasPrefix(text, "# ") {
			text = strings.TrimSpace(strings.TrimPrefix(text, "# "))
		} else if strings.HasPrefix(text, "- ") || strings.HasPrefix(text, "* ") {
			text = "• " + strings.TrimSpace(text[2:])
		}
		if text == "" {
			continue
		}
		content = append(content, []map[string]string{{"tag": "text", "text": text}})
	}
	if len(content) == 0 {
		content = append(content, []map[string]string{{"tag": "text", "text": strings.TrimSpace(markdown)}})
	}
	return map[string]any{
		"post": map[string]any{
			"zh_cn": map[string]any{
				"title":   "",
				"content": content,
			},
		},
	}
}

func (c *Client) DownloadImage(ctx context.Context, messageID string, imageKey string) (string, []byte, error) {
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return "", nil, err
	}
	endpoint := "https://open.larksuite.com/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/resources/" + url.PathEscape(imageKey) + "?type=image"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 3*1024*1024))
	if resp.StatusCode >= 400 {
		return "", nil, fmt.Errorf("lark image download failed: %d %s", resp.StatusCode, string(raw))
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}
	return contentType, raw, nil
}

func (c *Client) AddReaction(ctx context.Context, messageID, emojiType string) (string, error) {
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{
		"reaction_type": map[string]string{"emoji_type": emojiType},
	})
	if err != nil {
		return "", err
	}
	endpoint := "https://open.larksuite.com/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/reactions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", fmt.Errorf("lark add reaction failed: %d %s", resp.StatusCode, string(raw))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if code, _ := int64Value(payload["code"]); code != 0 {
		return "", fmt.Errorf("lark add reaction failed: %v", payload)
	}
	data, _ := payload["data"].(map[string]any)
	reactionID, _ := data["reaction_id"].(string)
	return reactionID, nil
}

func (c *Client) RemoveReaction(ctx context.Context, messageID, reactionID string) error {
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	endpoint := "https://open.larksuite.com/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/reactions/" + url.PathEscape(reactionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("lark remove reaction failed: %d %s", resp.StatusCode, string(raw))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if code, _ := int64Value(payload["code"]); code != 0 {
		return fmt.Errorf("lark remove reaction failed: %v", payload)
	}
	return nil
}

func (c *Client) tenantAccessToken(ctx context.Context) (string, error) {
	requestBody, err := json.Marshal(map[string]string{"app_id": c.appID, "app_secret": c.appToken})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.larksuite.com/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(requestBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", fmt.Errorf("lark tenant_access_token failed: %d %s", resp.StatusCode, string(raw))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if code, _ := int64Value(payload["code"]); code != 0 {
		return "", fmt.Errorf("lark tenant_access_token failed: %v", payload)
	}
	token := stringValue(payload["tenant_access_token"])
	if token == "" {
		return "", fmt.Errorf("lark tenant_access_token missing")
	}
	return token, nil
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func int64Value(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case float64:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	default:
		return 0, false
	}
}
