package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentwechat "xiaoli/server/internal/agent/channel/wechat"
)

func TestParseBuiltinCommandRequiresLeadingSlash(t *testing.T) {
	cmd, ok := parseBuiltinCommand(" /skills  --verbose ")
	if !ok {
		t.Fatal("parseBuiltinCommand() ok = false, want true")
	}
	if cmd.Name != "skills" || cmd.Args != "--verbose" {
		t.Fatalf("command = %#v, want skills with args", cmd)
	}
	if _, ok := parseBuiltinCommand("hello /skills"); ok {
		t.Fatal("parseBuiltinCommand() accepted non-leading slash command")
	}
}

func TestLarkBuiltinModelCommandRepliesWithoutPipeline(t *testing.T) {
	cfg := testConfig()
	cfg.LarkAppID = "cli_test"
	cfg.LarkAppToken = "token_test"
	cfg.GoLLMModel = "test-llm"
	cfg.GoVLLMModel = "test-vllm"
	srv := NewServer(cfg)
	srv.conversation = &ConversationPipeline{
		chat: conversationChatFunc(func(ctx context.Context, turn ConversationTurn) (string, error) {
			t.Fatalf("builtin command should not call conversation pipeline: %#v", turn)
			return "", nil
		}),
	}

	replyBodies := make(chan string, 1)
	srv.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "tenant_access_token": "tenant-token"}), nil
		case "/open-apis/im/v1/messages/om_model/reply":
			raw, _ := io.ReadAll(req.Body)
			replyBodies <- string(raw)
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"message_id": "reply_model"}}), nil
		default:
			t.Fatalf("unexpected Lark request path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	req := httptest.NewRequest(http.MethodPost, "/lark/events", strings.NewReader(`{
		"schema":"2.0",
		"header":{
			"event_id":"evt_model",
			"event_type":"im.message.receive_v1",
			"app_id":"cli_test",
			"tenant_key":"tenant_1"
		},
		"event":{
			"sender":{"sender_type":"user","sender_id":{"open_id":"ou_user"}},
			"message":{
				"message_id":"om_model",
				"chat_id":"oc_chat",
				"message_type":"text",
				"content":"{\"text\":\"/model\"}"
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	replyBody := <-replyBodies
	if !strings.Contains(replyBody, "test-llm") || !strings.Contains(replyBody, "test-vllm") {
		t.Fatalf("reply body = %s, want model information", replyBody)
	}
}

func TestWechatBuiltinChannelCommandRepliesWithoutPipeline(t *testing.T) {
	var sent agentwechat.SendMessageRequest
	c, _ := fakeWechatClientSequence(t, &sent, fakeWechatResponse{
		path:   "/ilink/bot/sendmessage",
		status: http.StatusOK,
		body:   `{"ret":0}`,
	})

	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/bridge/devices" {
			t.Fatalf("path = %s, want /bridge/devices", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"devices": []map[string]any{{"device_id": "device-1", "mcp_ready": true}},
		}), nil
	})}
	cfg := testConfig()
	cfg.WeChatEnabled = true
	cfg.WeChatBotToken = "wechat-token"
	srv := NewServer(cfg)
	srv.bridge = NewBridgeClient("http://bridge.local", httpClient)
	srv.conversation = &ConversationPipeline{
		chat: conversationChatFunc(func(ctx context.Context, turn ConversationTurn) (string, error) {
			t.Fatalf("builtin command should not call conversation pipeline: %#v", turn)
			return "", nil
		}),
	}

	srv.handleWechatMessage(context.Background(), c, &agentwechat.Message{
		FromUserID:   "user-1",
		ToUserID:     "bot-1",
		ContextToken: "ctx-1",
		MessageType:  agentwechat.MsgUser,
		ItemList: []agentwechat.MsgItem{
			{Type: agentwechat.ItemText, TextItem: &agentwechat.TextItem{Text: "/channel"}},
		},
	})

	if sent.Msg == nil || len(sent.Msg.ItemList) == 0 || sent.Msg.ItemList[0].TextItem == nil {
		t.Fatalf("sent message = %#v, want text reply", sent.Msg)
	}
	text := sent.Msg.ItemList[0].TextItem.Text
	if !strings.Contains(text, "esp32:device-1") || !strings.Contains(text, "wechat:bot") {
		t.Fatalf("reply text = %q, want channel list", text)
	}
}

func TestBuiltinSkillsCommandListsConfiguredSkills(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "holiday", "假期查询", "holiday body")
	cfg := testConfig()
	cfg.SkillRoots = []string{root}
	cfg.EnabledSkills = []string{"*"}
	srv := NewServer(cfg)

	reply, handled := srv.handleBuiltinCommand(context.Background(), ChannelLarkText, "/skills")

	if !handled {
		t.Fatal("handleBuiltinCommand() handled = false, want true")
	}
	if !strings.Contains(reply, "holiday") || !strings.Contains(reply, "假期查询") {
		t.Fatalf("reply = %q, want listed skill", reply)
	}
}

func TestBuiltinCommandUnknownIsNotHandled(t *testing.T) {
	srv := NewServer(testConfig())

	reply, handled := srv.handleBuiltinCommand(context.Background(), ChannelLarkText, "/unknown")

	if handled || reply != "" {
		encoded, _ := json.Marshal(reply)
		t.Fatalf("unknown command handled=%v reply=%s, want passthrough", handled, encoded)
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
