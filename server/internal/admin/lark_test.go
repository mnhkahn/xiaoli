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

	"github.com/mnhkahn/gogogo/logger"
)

func TestLoadConfigEnablesLarkOnlyFromAppIDAndToken(t *testing.T) {
	t.Setenv("LARK_APP_ID", "cli_test")
	t.Setenv("LARK_APP_TOKEN", "token_test")
	t.Setenv("LARK_APP_SECRET", "legacy_secret_must_not_be_used")

	cfg := LoadConfig()

	if cfg.LarkAppID != "cli_test" {
		t.Fatalf("LarkAppID = %q, want cli_test", cfg.LarkAppID)
	}
	if cfg.LarkAppToken != "token_test" {
		t.Fatalf("LarkAppToken = %q, want token_test", cfg.LarkAppToken)
	}
	if !cfg.LarkEnabled() {
		t.Fatal("LarkEnabled() = false, want true when LARK_APP_ID and LARK_APP_TOKEN are set")
	}
}

func TestLoadConfigDoesNotEnableLarkFromLegacySecret(t *testing.T) {
	t.Setenv("LARK_APP_ID", "cli_test")
	t.Setenv("LARK_APP_TOKEN", "")
	t.Setenv("LARK_APP_SECRET", "legacy_secret_must_not_enable_lark")

	cfg := LoadConfig()

	if cfg.LarkEnabled() {
		t.Fatal("LarkEnabled() = true, want false without LARK_APP_TOKEN")
	}
}

func TestLarkChallengeRouteEnabledByAppCredentials(t *testing.T) {
	cfg := testConfig()
	cfg.LarkAppID = "cli_test"
	cfg.LarkAppToken = "token_test"
	srv := NewServer(cfg)

	req := httptest.NewRequest(http.MethodPost, "/lark/events", strings.NewReader(`{
		"schema":"2.0",
		"header":{"event_type":"url_verification"},
		"event":{"challenge":"challenge-value"}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["challenge"] != "challenge-value" {
		t.Fatalf("challenge = %q, want challenge-value", payload["challenge"])
	}
}

func TestLarkTextEventUsesSharedPipelineAndReplies(t *testing.T) {
	cfg := testConfig()
	cfg.LarkAppID = "cli_test"
	cfg.LarkAppToken = "token_test"
	srv := NewServer(cfg)

	turns := make(chan ConversationTurn, 1)
	srv.conversation = &ConversationPipeline{
		chat: conversationChatFunc(func(ctx context.Context, turn ConversationTurn) (string, error) {
			turns <- turn
			return "收到：" + turn.Text, nil
		}),
	}

	authBodies := make(chan map[string]string, 4)
	replyBodies := make(chan string, 1)
	srv.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode auth request: %v", err)
			}
			authBodies <- body
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "tenant_access_token": "tenant-token"}), nil
		case "/open-apis/im/v1/messages/om_123/reply":
			if got := req.Header.Get("Authorization"); got != "Bearer tenant-token" {
				t.Fatalf("Authorization = %q, want Bearer tenant-token", got)
			}
			raw, _ := io.ReadAll(req.Body)
			replyBodies <- string(raw)
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"message_id": "reply_1"}}), nil
		case "/open-apis/im/v1/messages/om_123/reactions":
			if req.Method != http.MethodPost {
				t.Fatalf("reaction method = %s, want POST", req.Method)
			}
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"reaction_id": "reaction_1"}}), nil
		case "/open-apis/im/v1/messages/om_123/reactions/reaction_1":
			if req.Method != http.MethodDelete {
				t.Fatalf("remove reaction method = %s, want DELETE", req.Method)
			}
			return jsonResponse(http.StatusOK, map[string]any{"code": 0}), nil
		default:
			t.Fatalf("unexpected Lark request path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	req := httptest.NewRequest(http.MethodPost, "/lark/events", strings.NewReader(`{
		"schema":"2.0",
		"header":{
			"event_id":"evt_1",
			"event_type":"im.message.receive_v1",
			"app_id":"cli_test",
			"tenant_key":"tenant_1"
		},
		"event":{
			"sender":{
				"sender_type":"user",
				"sender_id":{"open_id":"ou_user"}
			},
			"message":{
				"message_id":"om_123",
				"chat_id":"oc_chat",
				"message_type":"text",
				"content":"{\"text\":\"你好\"}"
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	turn := <-turns
	if turn.Channel != ChannelLarkText {
		t.Fatalf("turn.Channel = %q, want %q", turn.Channel, ChannelLarkText)
	}
	if turn.Formatter == nil {
		t.Fatal("turn.Formatter is nil, want Lark formatter")
	}
	if instruction := turn.Formatter.Instruction(); !strings.Contains(instruction, "飞书") || !strings.Contains(instruction, "Markdown") {
		t.Fatalf("turn formatter instruction = %q, want Lark markdown instruction", instruction)
	}
	if turn.ConversationID != "lark:oc_chat:ou_user" {
		t.Fatalf("turn.ConversationID = %q, want lark:oc_chat:ou_user", turn.ConversationID)
	}
	if turn.Text != "你好" {
		t.Fatalf("turn.Text = %q, want 你好", turn.Text)
	}
	if turn.SendTarget.Channel != "lark" || turn.SendTarget.UserID != "ou_user" || turn.SendTarget.ChatID != "oc_chat" || turn.SendTarget.ReplyToMessageID != "om_123" {
		t.Fatalf("turn.SendTarget = %#v, want current Lark target", turn.SendTarget)
	}
	authBody := <-authBodies
	if authBody["app_id"] != "cli_test" || authBody["app_secret"] != "token_test" {
		t.Fatalf("auth body = %#v, want app_id and LARK_APP_TOKEN as app_secret", authBody)
	}
	replyBody := <-replyBodies
	if !strings.Contains(replyBody, `"msg_type":"post"`) || !strings.Contains(replyBody, `收到：你好`) {
		t.Fatalf("reply body = %s, want post reply with pipeline answer", replyBody)
	}
}

func TestLarkSkillSlashCommandRewritesTextForPipeline(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "holiday", "假期查询", "holiday body")

	cfg := testConfig()
	cfg.LarkAppID = "cli_test"
	cfg.LarkAppToken = "token_test"
	cfg.SkillRoots = []string{root}
	cfg.EnabledSkills = []string{"*"}
	srv := NewServer(cfg)

	turns := make(chan ConversationTurn, 1)
	srv.conversation = &ConversationPipeline{
		chat: conversationChatFunc(func(ctx context.Context, turn ConversationTurn) (string, error) {
			turns <- turn
			return "收到", nil
		}),
	}

	srv.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "tenant_access_token": "tenant-token"}), nil
		case "/open-apis/im/v1/messages/om_skill/reply":
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"message_id": "reply_skill"}}), nil
		case "/open-apis/im/v1/messages/om_skill/reactions":
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"reaction_id": "reaction_skill"}}), nil
		case "/open-apis/im/v1/messages/om_skill/reactions/reaction_skill":
			return jsonResponse(http.StatusOK, map[string]any{"code": 0}), nil
		default:
			t.Fatalf("unexpected Lark request path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	req := httptest.NewRequest(http.MethodPost, "/lark/events", strings.NewReader(`{
		"schema":"2.0",
		"header":{
			"event_id":"evt_skill",
			"event_type":"im.message.receive_v1",
			"app_id":"cli_test",
			"tenant_key":"tenant_1"
		},
		"event":{
			"sender":{"sender_type":"user","sender_id":{"open_id":"ou_user"}},
			"message":{
				"message_id":"om_skill",
				"chat_id":"oc_chat",
				"message_type":"text",
				"content":"{\"text\":\"/holiday 2026-10-01\"}"
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	turn := <-turns
	for _, want := range []string{"holiday", "2026-10-01", "Slash"} {
		if !strings.Contains(turn.Text, want) {
			t.Fatalf("turn.Text = %q, want %q", turn.Text, want)
		}
	}
}

func TestLarkTextEventLogsIngressWithoutMessageContent(t *testing.T) {
	cfg := testConfig()
	cfg.LarkAppID = "cli_test"
	cfg.LarkAppToken = "token_test"
	srv := NewServer(cfg)

	srv.conversation = &ConversationPipeline{
		chat: conversationChatFunc(func(ctx context.Context, turn ConversationTurn) (string, error) {
			return "收到", nil
		}),
	}
	srv.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "tenant_access_token": "tenant-token"}), nil
		case "/open-apis/im/v1/messages/om_log/reply":
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"message_id": "reply_log"}}), nil
		case "/open-apis/im/v1/messages/om_log/reactions":
			if req.Method != http.MethodPost {
				t.Fatalf("reaction method = %s, want POST", req.Method)
			}
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"reaction_id": "reaction_log"}}), nil
		case "/open-apis/im/v1/messages/om_log/reactions/reaction_log":
			if req.Method != http.MethodDelete {
				t.Fatalf("remove reaction method = %s, want DELETE", req.Method)
			}
			return jsonResponse(http.StatusOK, map[string]any{"code": 0}), nil
		default:
			t.Fatalf("unexpected Lark request path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	var logs bytes.Buffer
	oldLogger := logger.StdLogger
	logger.StdLogger = logger.NewWriterLogger(&logs, 0, 2)
	t.Cleanup(func() {
		logger.StdLogger = oldLogger
	})

	req := httptest.NewRequest(http.MethodPost, "/lark/events", strings.NewReader(`{
		"schema":"2.0",
		"header":{
			"event_id":"evt_log",
			"event_type":"im.message.receive_v1",
			"app_id":"cli_test",
			"tenant_key":"tenant_1"
		},
		"event":{
			"sender":{
				"sender_type":"user",
				"sender_id":{"open_id":"ou_user"}
			},
			"message":{
				"message_id":"om_log",
				"chat_id":"oc_chat",
				"message_type":"text",
				"content":"{\"text\":\"不要进日志\"}"
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	output := logs.String()
	for _, want := range []string{
		"[I] [lark] event received type=im.message.receive_v1 event_id=evt_log app_id=cli_test tenant=tenant_1",
		"[I] [lark] message received event_id=evt_log chat=oc_chat message=om_log sender_type=user sender=ou_user message_type=text",
		"[I] [lark] message reply sent event_id=evt_log message=om_log chat=oc_chat",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("logs missing %q\nlogs:\n%s", want, output)
		}
	}
	for _, forbidden := range []string{"不要进日志", "token_test", "tenant-token"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("logs contain forbidden value %q\nlogs:\n%s", forbidden, output)
		}
	}
}

func TestLarkImageEventDownloadsAnalyzesAndReplies(t *testing.T) {
	cfg := testConfig()
	cfg.LarkAppID = "cli_test"
	cfg.LarkAppToken = "token_test"
	srv := NewServer(cfg)
	vision := &capturingVisionAnalyzer{answer: "图里有一只杯子。"}
	srv.deviceHub.vision = vision

	replyBodies := make(chan string, 1)
	srv.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "tenant_access_token": "tenant-token"}), nil
		case "/open-apis/im/v1/messages/om_img/resources/img_key_1":
			if req.Method != http.MethodGet {
				t.Fatalf("download method = %s, want GET", req.Method)
			}
			if req.URL.Query().Get("type") != "image" {
				t.Fatalf("download type = %q, want image", req.URL.Query().Get("type"))
			}
			if got := req.Header.Get("Authorization"); got != "Bearer tenant-token" {
				t.Fatalf("download Authorization = %q, want bearer token", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"image/png"}},
				Body:       io.NopCloser(strings.NewReader("png-bytes")),
			}, nil
		case "/open-apis/im/v1/messages/om_img/reply":
			raw, _ := io.ReadAll(req.Body)
			replyBodies <- string(raw)
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"message_id": "reply_img"}}), nil
		default:
			t.Fatalf("unexpected Lark request path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	req := httptest.NewRequest(http.MethodPost, "/lark/events", strings.NewReader(`{
		"schema":"2.0",
		"header":{
			"event_id":"evt_img",
			"event_type":"im.message.receive_v1",
			"app_id":"cli_test",
			"tenant_key":"tenant_1"
		},
		"event":{
			"sender":{
				"sender_type":"user",
				"sender_id":{"open_id":"ou_user"}
			},
			"message":{
				"message_id":"om_img",
				"chat_id":"oc_chat",
				"message_type":"image",
				"content":"{\"image_key\":\"img_key_1\"}"
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if vision.question != "请描述这张图片里的内容。" || vision.contentType != "image/png" || string(vision.image) != "png-bytes" {
		t.Fatalf("vision = question %q contentType %q image %q", vision.question, vision.contentType, string(vision.image))
	}
	replyBody := <-replyBodies
	if !strings.Contains(replyBody, `图里有一只杯子。`) {
		t.Fatalf("reply body = %s, want vision answer", replyBody)
	}
}

func TestLarkImageEventRunsPipelineWithVisionSummary(t *testing.T) {
	cfg := testConfig()
	cfg.LarkAppID = "cli_test"
	cfg.LarkAppToken = "token_test"
	srv := NewServer(cfg)
	vision := &capturingVisionAnalyzer{answer: "图里有一只杯子，旁边写着 hello。"}
	srv.deviceHub.vision = vision

	turns := make(chan ConversationTurn, 1)
	srv.conversation = &ConversationPipeline{
		chat: conversationChatFunc(func(ctx context.Context, turn ConversationTurn) (string, error) {
			turns <- turn
			return "基于图片回答", nil
		}),
	}

	replyBodies := make(chan string, 1)
	srv.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "tenant_access_token": "tenant-token"}), nil
		case "/open-apis/im/v1/messages/om_img_pipeline/resources/img_key_1":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"image/png"}},
				Body:       io.NopCloser(strings.NewReader("png-bytes")),
			}, nil
		case "/open-apis/im/v1/messages/om_img_pipeline/reply":
			raw, _ := io.ReadAll(req.Body)
			replyBodies <- string(raw)
			return jsonResponse(http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"message_id": "reply_img"}}), nil
		default:
			t.Fatalf("unexpected Lark request path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	req := httptest.NewRequest(http.MethodPost, "/lark/events", strings.NewReader(`{
		"schema":"2.0",
		"header":{
			"event_id":"evt_img_pipeline",
			"event_type":"im.message.receive_v1",
			"app_id":"cli_test",
			"tenant_key":"tenant_1"
		},
		"event":{
			"sender":{"sender_type":"user","sender_id":{"open_id":"ou_user"}},
			"message":{
				"message_id":"om_img_pipeline",
				"chat_id":"oc_chat",
				"message_type":"image",
				"content":"{\"image_key\":\"img_key_1\",\"text\":\"这是什么？\"}"
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if vision.question != "这是什么？" {
		t.Fatalf("vision question = %q, want caption question", vision.question)
	}
	turn := <-turns
	if turn.ConversationID != "lark:oc_chat:ou_user" {
		t.Fatalf("turn.ConversationID = %q, want lark:oc_chat:ou_user", turn.ConversationID)
	}
	for _, want := range []string{"用户发来一张图片", "用户附言：这是什么？", "图片识别结果：图里有一只杯子"} {
		if !strings.Contains(turn.Text, want) {
			t.Fatalf("turn.Text = %q, want it to contain %q", turn.Text, want)
		}
	}
	replyBody := <-replyBodies
	if !strings.Contains(replyBody, "基于图片回答") {
		t.Fatalf("reply body = %s, want pipeline reply", replyBody)
	}
}

type capturingVisionAnalyzer struct {
	answer      string
	question    string
	contentType string
	image       []byte
}

func (f *capturingVisionAnalyzer) Analyze(ctx context.Context, question string, contentType string, image []byte) (string, error) {
	f.question = question
	f.contentType = contentType
	f.image = append([]byte(nil), image...)
	return f.answer, nil
}
