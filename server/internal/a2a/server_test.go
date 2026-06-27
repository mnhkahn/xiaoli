package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/stretchr/testify/assert"
)

type staticPipeline struct {
	response string
}

func (m *staticPipeline) Run(ctx context.Context, turn ConversationTurn) (ConversationReply, error) {
	return ConversationReply{Text: m.response}, nil
}

func buildJSONRPCReq(method string, params any) []byte {
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "test",
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}
	b, _ := json.Marshal(body)
	return b
}

func buildSendMessageParams(text string) map[string]any {
	return map[string]any{
		"message": map[string]any{
			"parts":     []map[string]any{{"text": text}},
			"role":      "ROLE_USER",
			"messageId": "msg_1",
		},
	}
}

func TestNewServer_AuthRequired(t *testing.T) {
	pipeline := &staticPipeline{response: "hello"}
	executor := NewExecutor(pipeline, 2000)
	server := NewServer(ServerConfig{
		Auth:           A2AConfig{APIKeys: map[string]string{"partner_a": "secret_a"}},
		RateLimit:      RateLimitConfig{PerKeyLimit: 1000, GlobalLimit: 1000},
		MaxInputChars:  2000,
		TimeoutSeconds: 60,
		TaskTTLSeconds: 1800,
	}, executor)

	req := httptest.NewRequest("POST", "/a2a", bytes.NewReader(buildJSONRPCReq("SendMessage", buildSendMessageParams("hi"))))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestNewServer_InvalidTokenRejected(t *testing.T) {
	pipeline := &staticPipeline{response: "hello"}
	executor := NewExecutor(pipeline, 2000)
	server := NewServer(ServerConfig{
		Auth:           A2AConfig{APIKeys: map[string]string{"partner_a": "secret_a"}},
		RateLimit:      RateLimitConfig{PerKeyLimit: 1000, GlobalLimit: 1000},
		MaxInputChars:  2000,
		TimeoutSeconds: 60,
		TaskTTLSeconds: 1800,
	}, executor)

	req := httptest.NewRequest("POST", "/a2a", bytes.NewReader(buildJSONRPCReq("SendMessage", buildSendMessageParams("hi"))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong_token")
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestNewServer_ValidTokenWorks(t *testing.T) {
	pipeline := &staticPipeline{response: "hello from pipeline"}
	executor := NewExecutor(pipeline, 2000)
	server := NewServer(ServerConfig{
		Auth:           A2AConfig{APIKeys: map[string]string{"partner_a": "secret_a"}},
		RateLimit:      RateLimitConfig{PerKeyLimit: 1000, GlobalLimit: 1000},
		MaxInputChars:  2000,
		TimeoutSeconds: 60,
		TaskTTLSeconds: 1800,
	}, executor)

	req := httptest.NewRequest("POST", "/a2a", bytes.NewReader(buildJSONRPCReq("SendMessage", buildSendMessageParams("hi"))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret_a")
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.Nil(t, resp["error"])
	result := resp["result"].(map[string]any)
	task := result["task"].(map[string]any)
	status := task["status"].(map[string]any)
	assert.Equal(t, string(a2a.TaskStateCompleted), status["state"])
}

func TestNewServer_OversizedInputRejected(t *testing.T) {
	pipeline := &staticPipeline{response: "hello"}
	executor := NewExecutor(pipeline, 10) // 10 char limit
	server := NewServer(ServerConfig{
		Auth:           A2AConfig{APIKeys: map[string]string{"partner_a": "secret_a"}},
		RateLimit:      RateLimitConfig{PerKeyLimit: 1000, GlobalLimit: 1000},
		MaxInputChars:  10,
		TimeoutSeconds: 60,
		TaskTTLSeconds: 1800,
	}, executor)

	longText := "this is way more than ten characters"
	req := httptest.NewRequest("POST", "/a2a", bytes.NewReader(buildJSONRPCReq("SendMessage", buildSendMessageParams(longText))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret_a")
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NotNil(t, resp["error"])
}

func TestNewServer_ListTasksReturnsError(t *testing.T) {
	pipeline := &staticPipeline{response: "hello"}
	executor := NewExecutor(pipeline, 2000)
	server := NewServer(ServerConfig{
		Auth:           A2AConfig{APIKeys: map[string]string{"partner_a": "secret_a"}},
		RateLimit:      RateLimitConfig{PerKeyLimit: 1000, GlobalLimit: 1000},
		MaxInputChars:  2000,
		TimeoutSeconds: 60,
		TaskTTLSeconds: 1800,
	}, executor)

	req := httptest.NewRequest("POST", "/a2a", bytes.NewReader(buildJSONRPCReq("ListTasks", map[string]any{})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret_a")
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]any
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Contains(t, resp, "error")
	errMap, ok := resp["error"].(map[string]any)
	assert.True(t, ok)
	msg, _ := errMap["message"].(string)
	assert.Contains(t, msg, "not supported")
}

func TestNewServer_TaskPartitionedByKeyID(t *testing.T) {
	pipeline := &staticPipeline{response: "hello from pipeline"}
	executor := NewExecutor(pipeline, 2000)
	server := NewServer(ServerConfig{
		Auth:           A2AConfig{APIKeys: map[string]string{"partner_a": "secret_a", "partner_b": "secret_b"}},
		RateLimit:      RateLimitConfig{PerKeyLimit: 1000, GlobalLimit: 1000},
		MaxInputChars:  2000,
		TimeoutSeconds: 60,
		TaskTTLSeconds: 1800,
	}, executor)

	// partner_a sends a message
	reqA := httptest.NewRequest("POST", "/a2a", bytes.NewReader(buildJSONRPCReq("SendMessage", buildSendMessageParams("hi from a"))))
	reqA.Header.Set("Content-Type", "application/json")
	reqA.Header.Set("Authorization", "Bearer secret_a")
	rrA := httptest.NewRecorder()
	server.ServeHTTP(rrA, reqA)
	assert.Equal(t, http.StatusOK, rrA.Code)

	var respA map[string]any
	json.Unmarshal(rrA.Body.Bytes(), &respA)
	assert.Nil(t, respA["error"])
	resultA := respA["result"].(map[string]any)
	taskA := resultA["task"].(map[string]any)
	taskID := taskA["id"]

	// partner_b tries to get partner_a's task - must fail with NotFound
	getParams := map[string]any{"id": taskID}
	reqB := httptest.NewRequest("POST", "/a2a", bytes.NewReader(buildJSONRPCReq("GetTask", getParams)))
	reqB.Header.Set("Content-Type", "application/json")
	reqB.Header.Set("Authorization", "Bearer secret_b")
	rrB := httptest.NewRecorder()
	server.ServeHTTP(rrB, reqB)

	assert.Equal(t, http.StatusOK, rrB.Code)
	var respB map[string]any
	json.Unmarshal(rrB.Body.Bytes(), &respB)
	assert.NotNil(t, respB["error"], "partner_b should not access partner_a's task")
}

func TestNewServer_TimeoutMiddlewareSet(t *testing.T) {
	pipeline := &staticPipeline{response: "fast response"}
	executor := NewExecutor(pipeline, 2000)
	server := NewServer(ServerConfig{
		Auth:           A2AConfig{APIKeys: map[string]string{"partner_a": "secret_a"}},
		RateLimit:      RateLimitConfig{PerKeyLimit: 1000, GlobalLimit: 1000},
		MaxInputChars:  2000,
		TimeoutSeconds: 60,
		TaskTTLSeconds: 1800,
	}, executor)

	req := httptest.NewRequest("POST", "/a2a", bytes.NewReader(buildJSONRPCReq("SendMessage", buildSendMessageParams("hi"))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret_a")
	rr := httptest.NewRecorder()

	start := time.Now()
	server.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, elapsed < 5*time.Second, "should not timeout on fast response")
}
