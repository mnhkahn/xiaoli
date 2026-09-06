package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGenerateTextWithoutToolsAllowsSlowResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Role, Content string } `json:"messages"`
			Tools    []json.RawMessage                `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if len(req.Tools) != 0 || len(req.Messages) != 2 {
			t.Errorf("request includes unexpected context: %+v", req)
		} else if req.Messages[0].Content != "commit rules" || req.Messages[1].Content != "git diff" {
			t.Errorf("wrong messages: %+v", req.Messages)
		}
		// Exceed the ordinary chat client's 15-second header timeout.
		time.Sleep(16 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"fix: 修复生成\n\n- 保留流程"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	cfg := Config{LLMModel: "test-model", LLMURL: server.URL, LLMAPIKey: "test", LLMTimeout: time.Minute}
	a := &Agent{cfg: cfg, modelSelector: newModelSelector(cfg)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := a.GenerateText(ctx, "commit rules", "git diff")
	if err != nil || got != "fix: 修复生成\n\n- 保留流程" {
		t.Fatalf("GenerateText = %q, %v", got, err)
	}
}

func TestGenerateTextPreservesQuotaErrorWithoutInternalRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"Daily limit reached","type":"rate_limit","code":"429"}}`)
	}))
	defer server.Close()
	cfg := Config{LLMModel: "test-model", LLMURL: server.URL, LLMAPIKey: "test"}
	a := &Agent{cfg: cfg, modelSelector: newModelSelector(cfg)}
	_, err := a.GenerateText(context.Background(), "rules", "diff")
	if err == nil || !strings.Contains(err.Error(), "Daily limit reached") || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
	if IsRetryableGenerationError(err) {
		t.Fatal("daily quota error should not retry")
	}
}

func TestIsRetryableGenerationError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"canceled", context.Canceled, false},
		{"timeout", context.DeadlineExceeded, true},
		{"daily quota", errors.New("status code: 429 Daily limit reached"), false},
		{"quota", errors.New("status code: 429 insufficient_quota"), false},
		{"auth", errors.New("status code: 401 invalid API key"), false},
		{"bad request", errors.New("status code: 400 invalid model"), false},
		{"temporary rate limit", errors.New("status code: 429 Too Many Requests"), true},
		{"server", errors.New("status code: 503"), true},
		{"empty choices", errors.New("received empty choices from OpenAI API response"), true},
		{"empty content", errors.New("model returned empty response"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetryableGenerationError(tc.err); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
