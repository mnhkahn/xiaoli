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
	"sync"
	"time"
)

type larkToken struct {
	token     string
	expiresAt time.Time
}

type Client struct {
	appID        string
	appToken     string
	httpClient   *http.Client
	tokenCache   larkToken
	tokenCacheMu sync.Mutex
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

func (c *Client) ReplyCard(ctx context.Context, messageID string, card map[string]any) error {
	content, err := json.Marshal(card)
	if err != nil {
		return err
	}
	return c.reply(ctx, messageID, "interactive", string(content))
}

func (c *Client) ReplyText(ctx context.Context, messageID string, text string) error {
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	return c.reply(ctx, messageID, "text", string(content))
}

func (c *Client) ReplyPost(ctx context.Context, messageID string, title string, markdown string) error {
	content, err := json.Marshal(markdownToPostContent(title, markdown))
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

// CreateTextMessage 主动给用户发文本消息，receiveIDType 默认为 open_id
func (c *Client) CreateTextMessage(ctx context.Context, receiveID string, text string) error {
	return c.createMessage(ctx, receiveID, "text", map[string]string{"text": text})
}

// createMessage 主动发消息通用接口
func (c *Client) createMessage(ctx context.Context, receiveID string, msgType string, content any) error {
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   msgType,
		"content":    string(contentJSON),
	})
	if err != nil {
		return err
	}
	endpoint := "https://open.larksuite.com/open-apis/im/v1/messages?receive_id_type=open_id"
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
		return fmt.Errorf("lark create message failed: %d %s", resp.StatusCode, string(raw))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if code, _ := int64Value(payload["code"]); code != 0 {
		return fmt.Errorf("lark create message failed: %v", payload)
	}
	return nil
}

func markdownToPostContent(title string, markdown string) map[string]any {
	if title == "" {
		title = "消息"
	}
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
		content = append(content, parsePostLine(text))
	}
	if len(content) == 0 {
		content = append(content, parsePostLine(strings.TrimSpace(markdown)))
	}
	return map[string]any{
		"zh_cn": map[string]any{
			"title":   title,
			"content": content,
		},
	}
}

func parsePostLine(text string) []map[string]string {
	items := parseInline(text)
	if len(items) > 0 {
		return items
	}
	return []map[string]string{{"tag": "text", "text": text}}
}

func parseInline(text string) []map[string]string {
	var items []map[string]string
	pos := 0
	for pos < len(text) {
		// bold **text** → text tag only (Lark post doesn't support bold tag)
		if strings.HasPrefix(text[pos:], "**") {
			end := strings.Index(text[pos+2:], "**")
			if end >= 0 {
				content := text[pos+2 : pos+2+end]
				if content != "" {
					items = append(items, map[string]string{"tag": "text", "text": content})
				}
				pos += 2 + end + 2
				continue
			}
			// unclosed ** → plain text
			start := pos
			pos += 2
			items = append(items, map[string]string{"tag": "text", "text": text[start:pos]})
			continue
		}
		// link [text](url)
		if text[pos] == '[' {
			closeB := strings.Index(text[pos+1:], "](")
			if closeB >= 0 {
				rest := text[pos+1+closeB+2:]
				closeP := strings.Index(rest, ")")
				if closeP >= 0 {
					linkText := text[pos+1 : pos+1+closeB]
					url := rest[:closeP]
					if linkText != "" && url != "" {
						items = append(items, map[string]string{"tag": "a", "text": linkText, "href": url})
						pos += 1 + closeB + 2 + closeP + 1
						continue
					}
				}
			}
		}
		// plain text run
		start := pos
		for pos < len(text) && !strings.HasPrefix(text[pos:], "**") && text[pos] != '[' {
			pos++
		}
		if pos > start {
			items = append(items, map[string]string{"tag": "text", "text": text[start:pos]})
		}
		if pos == start {
			items = append(items, map[string]string{"tag": "text", "text": text[pos : pos+1]})
			pos++
		}
	}
	return items
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
	c.tokenCacheMu.Lock()
	if c.tokenCache.token != "" && time.Now().Before(c.tokenCache.expiresAt) {
		defer c.tokenCacheMu.Unlock()
		return c.tokenCache.token, nil
	}
	c.tokenCacheMu.Unlock()

	tokenCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	requestBody, err := json.Marshal(map[string]string{"app_id": c.appID, "app_secret": c.appToken})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(tokenCtx, http.MethodPost, "https://open.larksuite.com/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(requestBody))
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

	expire := 7200
	if e, ok := int64Value(payload["expire"]); ok && e > 0 {
		expire = int(e)
	}
	c.tokenCacheMu.Lock()
	c.tokenCache.token = token
	c.tokenCache.expiresAt = time.Now().Add(time.Duration(expire-300) * time.Second)
	c.tokenCacheMu.Unlock()
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
