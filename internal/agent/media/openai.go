package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

type ASRConfig struct {
	URL     string
	APIKey  string
	Model   string
	Timeout time.Duration
}

type OpenAITranscriber struct {
	url    string
	apiKey string
	model  string
	client *http.Client
}

func NewOpenAITranscriber(cfg ASRConfig) SpeechRecognizer {
	if cfg.APIKey == "" {
		return nil
	}
	return &OpenAITranscriber{
		url:    cfg.URL,
		apiKey: cfg.APIKey,
		model:  cfg.Model,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

func (c *OpenAITranscriber) Transcribe(ctx context.Context, oggOpus []byte) (string, error) {
	if c == nil || c.apiKey == "" {
		return "", errors.New("ASR is not configured")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", c.model); err != nil {
		return "", err
	}
	_ = writer.WriteField("response_format", "json")
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="speech.ogg"`)
	header.Set("Content-Type", "audio/ogg")
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(oggOpus); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		return "", fmt.Errorf("ASR request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(errorBody)))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	text := strings.TrimSpace(StringValue(payload["text"]))
	if text == "" {
		return "", errors.New("ASR returned empty text")
	}
	return text, nil
}

type VisionConfig struct {
	URL     string
	APIKey  string
	Model   string
	Timeout time.Duration
}

type OpenAIVisionClient struct {
	url    string
	apiKey string
	model  string
	client *http.Client
}

func NewOpenAIVisionClient(cfg VisionConfig) VisionAnalyzer {
	if cfg.APIKey == "" {
		return nil
	}
	return &OpenAIVisionClient{
		url:    cfg.URL,
		apiKey: cfg.APIKey,
		model:  cfg.Model,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

func (c *OpenAIVisionClient) Analyze(ctx context.Context, question string, contentType string, image []byte) (string, error) {
	if c == nil || c.apiKey == "" {
		return "", errors.New("vision model is not configured")
	}
	if question == "" {
		question = "请描述这张图片里的内容。"
	}
	dataURL := "data:" + NormalizeImageContentType(contentType, "image/jpeg") + ";base64," + base64.StdEncoding.EncodeToString(image)
	payload := map[string]any{
		"model": c.model,
		"messages": []map[string]any{
			{"role": "system", "content": "你是一个视觉助手。回答要简短、直接，适合通过语音播放。"},
			{"role": "user", "content": []map[string]any{
				{"type": "text", "text": question},
				{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
			}},
		},
		"temperature": 0.2,
		"max_tokens":  300,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 800))
		return "", fmt.Errorf("vision request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(errorBody)))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("vision model returned no choices")
	}
	answer := strings.TrimSpace(ContentText(parsed.Choices[0].Message.Content))
	if answer == "" {
		return "", errors.New("vision model returned empty answer")
	}
	return answer, nil
}

func ContentText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text := StringValue(m["text"]); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return StringValue(value)
	}
}

func NormalizeImageContentType(contentType string, fallback string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch contentType {
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/png":
		return "image/png"
	case "image/webp":
		return "image/webp"
	default:
		if fallback != "" {
			return fallback
		}
		return "image/jpeg"
	}
}

func StringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
