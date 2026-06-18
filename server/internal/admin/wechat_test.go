package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWechatSendLocal(t *testing.T) {
	token := os.Getenv("WECHAT_BOT_TOKEN")
	toUserID := os.Getenv("WECHAT_TO_USER")
	contextToken := os.Getenv("WECHAT_CONTEXT_TOKEN")
	if token == "" || toUserID == "" {
		t.Skip("set WECHAT_BOT_TOKEN, WECHAT_TO_USER, WECHAT_CONTEXT_TOKEN")
	}

	baseURL := os.Getenv("WECHAT_BASE_URL")
	if baseURL == "" {
		baseURL = "https://ilinkai.weixin.qq.com"
	}

	c := &wechatClient{
		BaseURL: baseURL,
		Token:   token,
		HTTPDo:  http.DefaultClient.Do,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("send with empty FromUserID", func(t *testing.T) {
		err := wechatSendText(ctx, c, "", toUserID, contextToken, "本地测试消息（FromUserID为空）")
		if err != nil {
			t.Errorf("send: %v", err)
		}
	})

	t.Run("send with bot FromUserID", func(t *testing.T) {
		botID := os.Getenv("WECHAT_BOT_USER_ID")
		if botID == "" {
			t.Skip("set WECHAT_BOT_USER_ID to test with FromUserID")
		}
		err := wechatSendText(ctx, c, botID, toUserID, contextToken, "本地测试消息（FromUserID=botID）")
		if err != nil {
			t.Errorf("send: %v", err)
		}
	})

	t.Run("send typing indicator", func(t *testing.T) {
		wechatSendTyping(ctx, c, "", toUserID, "")
	})
}

func TestWechatSendTextLeavesFromUserIDEmpty(t *testing.T) {
	var sent wechatSendMsgReq
	c, _ := fakeWechatClientSequence(t, &sent, fakeWechatResponse{
		path:   "/ilink/bot/sendmessage",
		status: http.StatusOK,
		body:   `{"ret":0}`,
	})

	err := wechatSendText(context.Background(), c, "bot-user", "target-user", "ctx-token", "hello")
	if err != nil {
		t.Fatalf("wechatSendText() error = %v", err)
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

func TestWechatSendTextIncludesClientID(t *testing.T) {
	var sent map[string]any
	c, _ := fakeWechatClientSequence(t, &sent, fakeWechatResponse{
		path:   "/ilink/bot/sendmessage",
		status: http.StatusOK,
		body:   `{"ret":0}`,
	})

	err := wechatSendText(context.Background(), c, "bot-user", "target-user", "ctx-token", "hello")
	if err != nil {
		t.Fatalf("wechatSendText() error = %v", err)
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

func TestWechatSendTypingDoesNotSendMessagePayload(t *testing.T) {
	var sent any
	c, captured := fakeWechatClientSequence(t, &sent,
		fakeWechatResponse{
			path:   "/ilink/bot/getconfig",
			status: http.StatusOK,
			body:   `{"ret":0,"typing_ticket":"ticket-123"}`,
		},
		fakeWechatResponse{
			path:   "/ilink/bot/sendtyping",
			status: http.StatusOK,
			body:   `{"ret":0}`,
		},
	)

	err := wechatSendTyping(context.Background(), c, "bot-user", "target-user", "ctx-token")
	if err != nil {
		t.Fatalf("wechatSendTyping() error = %v", err)
	}
	var typingBody map[string]any
	if err := json.Unmarshal((*captured)[1].body, &typingBody); err != nil {
		t.Fatalf("decode sendtyping body: %v", err)
	}
	if _, ok := typingBody["msg"]; ok {
		t.Fatalf("sendtyping body = %#v, should not contain message payload", typingBody)
	}
	if _, ok := typingBody["from_user_id"]; ok {
		t.Fatalf("sendtyping body = %#v, should not contain from_user_id", typingBody)
	}
}

func TestWechatSendTypingUsesTypingAPI(t *testing.T) {
	var sent any
	c, captured := fakeWechatClientSequence(t, &sent,
		fakeWechatResponse{
			path:   "/ilink/bot/getconfig",
			status: http.StatusOK,
			body:   `{"ret":0,"typing_ticket":"ticket-123"}`,
		},
		fakeWechatResponse{
			path:   "/ilink/bot/sendtyping",
			status: http.StatusOK,
			body:   `{"ret":0}`,
		},
	)

	err := wechatSendTyping(context.Background(), c, "bot-user", "target-user", "ctx-token")
	if err != nil {
		t.Fatalf("wechatSendTyping() error = %v", err)
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
}

func TestWechatSendTypingReturnsAPIError(t *testing.T) {
	c, _ := fakeWechatClientSequence(t, nil, fakeWechatResponse{
		path:   "/ilink/bot/getconfig",
		status: http.StatusOK,
		body:   `{"ret":40001,"errmsg":"bad token"}`,
	})

	err := wechatSendTyping(context.Background(), c, "", "target-user", "ctx-token")
	if err == nil {
		t.Fatal("wechatSendTyping() error = nil, want API error")
	}
}

type fakeWechatResponse struct {
	path   string
	status int
	body   string
}

type capturedWechatRequest struct {
	path string
	body []byte
}

func fakeWechatClientSequence(t *testing.T, sent any, responses ...fakeWechatResponse) (*wechatClient, *[]capturedWechatRequest) {
	t.Helper()
	captured := []capturedWechatRequest{}
	call := 0
	return &wechatClient{
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
			captured = append(captured, capturedWechatRequest{path: req.URL.Path, body: raw})
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
	}, &captured
}
