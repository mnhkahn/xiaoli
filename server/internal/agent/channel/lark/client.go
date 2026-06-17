package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"msg_type": "text",
		"content":  string(content),
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
