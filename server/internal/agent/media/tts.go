package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type TTSConfig struct {
	URL            string
	APIKey         string
	Model          string
	Voice          string
	ResponseFormat string
	Timeout        time.Duration
	HTTPClient     *http.Client
}

type HTTPSpeechSynthesizer struct {
	url            string
	apiKey         string
	model          string
	voice          string
	responseFormat string
	client         *http.Client
}

func NewHTTPSpeechSynthesizer(cfg TTSConfig) SpeechSynthesizer {
	if cfg.APIKey == "" {
		return nil
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &HTTPSpeechSynthesizer{
		url:            cfg.URL,
		apiKey:         cfg.APIKey,
		model:          cfg.Model,
		voice:          cfg.Voice,
		responseFormat: strings.ToLower(strings.TrimSpace(cfg.ResponseFormat)),
		client:         client,
	}
}

func (s *HTTPSpeechSynthesizer) Synthesize(ctx context.Context, text string) (string, []byte, error) {
	if s == nil || s.apiKey == "" {
		return "", nil, errors.New("TTS is not configured")
	}
	if s.responseFormat != "opus" && s.responseFormat != "ogg" {
		return "", nil, fmt.Errorf("Go TTS must return Ogg Opus audio; set XIAOLI_GO_TTS_RESPONSE_FORMAT=opus, got %q", s.responseFormat)
	}
	payload := map[string]any{
		"model":           s.model,
		"voice":           s.voice,
		"input":           text,
		"response_format": "opus",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		return "", nil, fmt.Errorf("TTS request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(errorBody)))
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", nil, err
	}
	if len(audio) == 0 {
		return "", nil, errors.New("TTS returned empty audio")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "audio/ogg"
	}
	return NormalizeOggContentType(contentType), audio, nil
}

func NormalizeOggContentType(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	switch value {
	case "audio/ogg", "audio/opus", "application/ogg":
		return "audio/ogg"
	default:
		return "audio/ogg"
	}
}
