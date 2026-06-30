package wechat

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/base64"
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

func TestPollMessagesCarriesBufferAndDeliversUserMessagesOnly(t *testing.T) {
	c, captured := fakeClientSequence(t, nil,
		fakeResponse{
			path:   "/ilink/bot/getupdates",
			status: http.StatusOK,
			body:   `{"ret":0,"get_updates_buf":"next","msgs":[{"message_type":2}]}`,
		},
		fakeResponse{
			path:   "/ilink/bot/getupdates",
			status: http.StatusOK,
			body:   `{"ret":0,"msgs":[{"message_type":1,"from_user_id":"user-1","item_list":[{"type":1,"text_item":{"text":"hello"}}]}]}`,
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var got []*Message
	PollMessages(ctx, c, func(_ context.Context, msg *Message) {
		got = append(got, msg)
		cancel()
	})

	if len(got) != 1 {
		t.Fatalf("delivered messages = %d, want 1", len(got))
	}
	if got[0].FromUserID != "user-1" || got[0].Text() != "hello" {
		t.Fatalf("delivered message = %#v, want user text message", got[0])
	}
	if len(*captured) != 2 {
		t.Fatalf("captured requests = %d, want 2", len(*captured))
	}
	var second map[string]any
	if err := json.Unmarshal((*captured)[1].body, &second); err != nil {
		t.Fatalf("decode second poll body: %v", err)
	}
	if second["get_updates_buf"] != "next" {
		t.Fatalf("second poll body = %#v, want carried get_updates_buf", second)
	}
}

func TestDownloadImageUsesWeixinCDNAndDecryptsQueryMedia(t *testing.T) {
	key := []byte("0123456789abcdef")
	plaintext := []byte("fake-jpeg-bytes")
	encrypted := encryptAesECBPKCS7ForTest(t, plaintext, key)

	c := NewClient(ClientConfig{
		BaseURL: "https://wechat.test",
		Token:   "token",
		HTTPDo: func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet {
				t.Fatalf("request method = %s, want GET", req.Method)
			}
			if req.URL.Scheme != "https" || req.URL.Host != "novac2c.cdn.weixin.qq.com" || req.URL.Path != "/c2c/download" {
				t.Fatalf("request URL = %s, want Weixin CDN download URL", req.URL.String())
			}
			if got := req.URL.Query().Get("encrypted_query_param"); got != "fileid=abc&token=secret" {
				t.Fatalf("encrypted_query_param = %q, want original query", got)
			}
			for _, header := range []string{"Authorization", "AuthorizationType", "X-WECHAT-UIN"} {
				if got := req.Header.Get(header); got != "" {
					t.Fatalf("%s header = %q, want empty for CDN request", header, got)
				}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(encrypted)),
				Header:     make(http.Header),
			}, nil
		},
	})

	contentType, body, err := c.DownloadImage(context.Background(), &ImageItem{
		Media: &CDNMedia{
			EncryptQueryParam: "fileid=abc&token=secret",
			AESKey:            base64.StdEncoding.EncodeToString(key),
		},
	})
	if err != nil {
		t.Fatalf("DownloadImage() error = %v", err)
	}
	if contentType != "image/jpeg" {
		t.Fatalf("contentType = %q, want image/jpeg fallback", contentType)
	}
	if string(body) != string(plaintext) {
		t.Fatalf("body = %q, want decrypted plaintext %q", string(body), string(plaintext))
	}
}

func TestDownloadImagePrefersImageAESKeyHex(t *testing.T) {
	mediaKey := []byte("wrong-wrong-key!")
	imageKey := []byte("0123456789abcdef")
	plaintext := []byte("image-key-plaintext")
	encrypted := encryptAesECBPKCS7ForTest(t, plaintext, imageKey)

	c := NewClient(ClientConfig{
		HTTPDo: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(encrypted)),
				Header:     make(http.Header),
			}, nil
		},
	})

	_, body, err := c.DownloadImage(context.Background(), &ImageItem{
		AESKeyHex: "30313233343536373839616263646566",
		Media: &CDNMedia{
			EncryptQueryParam: "fileid=abc",
			AESKey:            base64.StdEncoding.EncodeToString(mediaKey),
		},
	})
	if err != nil {
		t.Fatalf("DownloadImage() error = %v", err)
	}
	if string(body) != string(plaintext) {
		t.Fatalf("body = %q, want plaintext decrypted with image aeskey", string(body))
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

func encryptAesECBPKCS7ForTest(t *testing.T, plaintext, key []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(append([]byte{}, plaintext...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(padded))
	for offset := 0; offset < len(padded); offset += aes.BlockSize {
		block.Encrypt(encrypted[offset:offset+aes.BlockSize], padded[offset:offset+aes.BlockSize])
	}
	return encrypted
}
