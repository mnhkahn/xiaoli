package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSendTextLeavesFromUserIDEmpty(t *testing.T) {
	var sent SendMessageRequest
	c, _ := fakeClientSequence(t, &sent, fakeResponse{
		path:   "/ilink/bot/sendmessage",
		status: http.StatusOK,
		body:   `{"ret":0}`,
	})

	err := SendText(context.Background(), c, "bot-user", "target-user", "ctx-token", "hello")
	if err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if sent.Msg == nil {
		t.Fatal("sent Msg is nil")
	}
	if sent.Msg.FromUserID != "" {
		t.Fatalf("FromUserID = %q, want empty", sent.Msg.FromUserID)
	}
	if sent.Msg.ToUserID != "target-user" || sent.Msg.ContextToken != "ctx-token" || sent.Msg.MessageState != 2 {
		t.Fatalf("sent Msg = %#v, want target user, context token, and final message state", sent.Msg)
	}
}

func TestSendTextIncludesClientID(t *testing.T) {
	var sent map[string]any
	c, _ := fakeClientSequence(t, &sent, fakeResponse{
		path:   "/ilink/bot/sendmessage",
		status: http.StatusOK,
		body:   `{"ret":0}`,
	})

	err := SendText(context.Background(), c, "bot-user", "target-user", "ctx-token", "hello")
	if err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	msg, ok := sent["msg"].(map[string]any)
	if !ok {
		t.Fatalf("sent msg = %#v, want object", sent["msg"])
	}
	clientID, _ := msg["client_id"].(string)
	if !strings.HasPrefix(clientID, "xiaoli-wechat-") {
		t.Fatalf("client_id = %q, want generated xiaoli-wechat-* id", clientID)
	}
}

func TestSendTypingUsesTypingAPI(t *testing.T) {
	var sent any
	c, captured := fakeClientSequence(t, &sent,
		fakeResponse{
			path:   "/ilink/bot/getconfig",
			status: http.StatusOK,
			body:   `{"ret":0,"typing_ticket":"ticket-123"}`,
		},
		fakeResponse{
			path:   "/ilink/bot/sendtyping",
			status: http.StatusOK,
			body:   `{"ret":0}`,
		},
	)

	err := SendTyping(context.Background(), c, "bot-user", "target-user", "ctx-token")
	if err != nil {
		t.Fatalf("SendTyping() error = %v", err)
	}
	if len(*captured) != 2 {
		t.Fatalf("captured requests = %d, want 2", len(*captured))
	}

	var getConfigBody map[string]any
	if err := json.Unmarshal((*captured)[0].body, &getConfigBody); err != nil {
		t.Fatalf("decode getconfig body: %v", err)
	}
	if getConfigBody["ilink_user_id"] != "target-user" || getConfigBody["context_token"] != "ctx-token" {
		t.Fatalf("getconfig body = %#v, want target user and context token", getConfigBody)
	}

	var typingBody map[string]any
	if err := json.Unmarshal((*captured)[1].body, &typingBody); err != nil {
		t.Fatalf("decode sendtyping body: %v", err)
	}
	if typingBody["ilink_user_id"] != "target-user" || typingBody["typing_ticket"] != "ticket-123" || typingBody["status"] != float64(1) {
		t.Fatalf("sendtyping body = %#v, want target user, ticket, typing status", typingBody)
	}
	if _, ok := typingBody["msg"]; ok {
		t.Fatalf("sendtyping body = %#v, should not contain message payload", typingBody)
	}
}

func TestSendTypingReturnsAPIError(t *testing.T) {
	c, _ := fakeClientSequence(t, nil, fakeResponse{
		path:   "/ilink/bot/getconfig",
		status: http.StatusOK,
		body:   `{"ret":40001,"errmsg":"bad token"}`,
	})

	err := SendTyping(context.Background(), c, "", "target-user", "ctx-token")
	if err == nil {
		t.Fatal("SendTyping() error = nil, want API error")
	}
}

type fakeResponse struct {
	path   string
	status int
	body   string
}

type capturedRequest struct {
	path string
	body []byte
}

func fakeClientSequence(t *testing.T, sent any, responses ...fakeResponse) (*Client, *[]capturedRequest) {
	t.Helper()
	captured := []capturedRequest{}
	call := 0
	return NewClient(ClientConfig{
		BaseURL: "https://wechat.test",
		Token:   "token",
		HTTPDo: func(req *http.Request) (*http.Response, error) {
			if call >= len(responses) {
				t.Fatalf("unexpected request path = %s", req.URL.Path)
			}
			response := responses[call]
			call++
			if req.URL.Path != response.path {
				t.Fatalf("request path = %s, want %s", req.URL.Path, response.path)
			}
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("ReadAll(request body) error = %v", err)
			}
			captured = append(captured, capturedRequest{path: req.URL.Path, body: raw})
			if sent != nil {
				if err := json.Unmarshal(raw, sent); err != nil {
					t.Fatalf("Unmarshal(request body) error = %v body=%s", err, string(raw))
				}
			}
			return &http.Response{
				StatusCode: response.status,
				Body:       io.NopCloser(bytes.NewBufferString(response.body)),
				Header:     make(http.Header),
			}, nil
		},
	}), &captured
}
