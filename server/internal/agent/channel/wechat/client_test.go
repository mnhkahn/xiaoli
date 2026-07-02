package wechat

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentchannel "github.com/mnhkahn/xiaoli-esp32/server/internal/agent/channel"
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

func TestSendImageAttachmentUploadsToCDNAndSendsImageMessage(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "photo.png")
	plaintext := []byte("png-bytes")
	if err := os.WriteFile(imagePath, plaintext, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	wantMD5 := md5.Sum(plaintext)

	var sent []SendMessageRequest
	var sawCDNUpload bool
	c := NewClient(ClientConfig{
		BaseURL: "https://wechat.test",
		Token:   "token",
		HTTPDo: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Host + req.URL.Path {
			case "wechat.test/ilink/bot/getuploadurl":
				if req.Header.Get("iLink-App-Id") != "bot" || req.Header.Get("iLink-App-ClientVersion") != "132102" {
					t.Fatalf("getuploadurl app headers = %q/%q, want official app id/client version", req.Header.Get("iLink-App-Id"), req.Header.Get("iLink-App-ClientVersion"))
				}
				rawBody, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("read getuploadurl body: %v", err)
				}
				var body GetUploadURLRequest
				if err := json.Unmarshal(rawBody, &body); err != nil {
					t.Fatalf("decode getuploadurl body: %v", err)
				}
				var raw map[string]any
				if err := json.Unmarshal(rawBody, &raw); err != nil {
					t.Fatalf("decode getuploadurl raw body: %v", err)
				}
				baseInfo, _ := raw["base_info"].(map[string]any)
				if baseInfo["channel_version"] != "2.4.6" || baseInfo["bot_agent"] != "OpenClaw" {
					t.Fatalf("getuploadurl base_info = %#v, want official channel version and bot agent", baseInfo)
				}
				if body.MediaType != UploadMediaTypeImage || body.ToUserID != "user-1" {
					t.Fatalf("getuploadurl body = %#v, want image upload for user-1", body)
				}
				if body.RawSize != len(plaintext) || body.FileSize != 16 || body.RawFileMD5 != hex.EncodeToString(wantMD5[:]) {
					t.Fatalf("getuploadurl body sizes/md5 = %#v, want raw size/md5 and padded size", body)
				}
				if body.FileKey == "" || len(body.AESKey) != 32 || !isHexString(body.AESKey) {
					t.Fatalf("getuploadurl body filekey/aeskey = %#v, want generated hex values", body)
				}
				return jsonHTTPResponse(http.StatusOK, map[string]any{
					"upload_full_url": "https://cdn.test/upload",
				}), nil
			case "cdn.test/upload":
				sawCDNUpload = true
				if req.Header.Get("Authorization") != "" || req.Header.Get("AuthorizationType") != "" {
					t.Fatalf("CDN upload auth headers = %q/%q, want empty", req.Header.Get("Authorization"), req.Header.Get("AuthorizationType"))
				}
				if req.Header.Get("Content-Type") != "application/octet-stream" {
					t.Fatalf("CDN upload content-type = %q, want application/octet-stream", req.Header.Get("Content-Type"))
				}
				ciphertext, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("read CDN upload body: %v", err)
				}
				if len(ciphertext) != 16 || bytes.Equal(ciphertext, plaintext) {
					t.Fatalf("CDN upload body = %x, want encrypted padded bytes", ciphertext)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(nil)),
					Header:     http.Header{"X-Encrypted-Param": []string{"download-param-1"}},
				}, nil
			case "wechat.test/ilink/bot/sendmessage":
				var body SendMessageRequest
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatalf("decode sendmessage body: %v", err)
				}
				sent = append(sent, body)
				return jsonHTTPResponse(http.StatusOK, map[string]any{"ret": 0}), nil
			default:
				t.Fatalf("unexpected request URL = %s", req.URL.String())
				return nil, nil
			}
		},
	})

	err := c.SendImageAttachment(context.Background(), "user-1", "ctx-1", imagePath, "看图")
	if err != nil {
		t.Fatalf("SendImageAttachment() error = %v", err)
	}
	if !sawCDNUpload {
		t.Fatal("CDN upload was not called")
	}
	if len(sent) != 2 {
		t.Fatalf("sent messages = %d, want caption and image", len(sent))
	}
	if got := sent[0].Msg.ItemList[0].TextItem.Text; got != "看图" {
		t.Fatalf("caption text = %q, want 看图", got)
	}
	item := sent[1].Msg.ItemList[0]
	if item.Type != ItemImage || item.ImageItem == nil || item.ImageItem.Media == nil {
		t.Fatalf("image item = %#v, want image media item", item)
	}
	if item.ImageItem.Media.EncryptQueryParam != "download-param-1" || item.ImageItem.Media.EncryptType != 1 {
		t.Fatalf("image media = %#v, want download param and encrypt type", item.ImageItem.Media)
	}
	if item.ImageItem.Media.AESKey == "" || item.ImageItem.MidSize != 16 {
		t.Fatalf("image aes/mid_size = %#v, want aes key and ciphertext size", item.ImageItem)
	}
	decodedAESKey, err := base64.StdEncoding.DecodeString(item.ImageItem.Media.AESKey)
	if err != nil {
		t.Fatalf("decode image aes key: %v", err)
	}
	if len(decodedAESKey) != aes.BlockSize {
		t.Fatalf("image aes key decoded length = %d, want raw AES key length %d", len(decodedAESKey), aes.BlockSize)
	}
}

func TestSenderSendAttachmentSupportsImageAndFile(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "photo.png")
	filePath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(imagePath, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var uploadMediaTypes []UploadMediaType
	var sentItemTypes []ItemType
	var sentFileAESKey string
	c := NewClient(ClientConfig{
		BaseURL: "https://wechat.test",
		Token:   "token",
		HTTPDo: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Host + req.URL.Path {
			case "wechat.test/ilink/bot/getuploadurl":
				var body GetUploadURLRequest
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatalf("decode getuploadurl body: %v", err)
				}
				uploadMediaTypes = append(uploadMediaTypes, body.MediaType)
				return jsonHTTPResponse(http.StatusOK, map[string]any{"upload_full_url": "https://cdn.test/upload"}), nil
			case "cdn.test/upload":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(nil)),
					Header:     http.Header{"X-Encrypted-Param": []string{"download-param"}},
				}, nil
			case "wechat.test/ilink/bot/sendmessage":
				var body SendMessageRequest
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatalf("decode sendmessage body: %v", err)
				}
				if len(body.Msg.ItemList) == 1 && body.Msg.ItemList[0].Type != ItemText {
					item := body.Msg.ItemList[0]
					sentItemTypes = append(sentItemTypes, item.Type)
					if item.Type == ItemFile && item.FileItem != nil && item.FileItem.Media != nil {
						sentFileAESKey = item.FileItem.Media.AESKey
					}
				}
				return jsonHTTPResponse(http.StatusOK, map[string]any{"ret": 0}), nil
			default:
				t.Fatalf("unexpected request URL = %s", req.URL.String())
				return nil, nil
			}
		},
	})
	sender := NewSender(c)
	target := SendTargetForTest("user-1", "ctx-1")

	if err := sender.SendAttachment(context.Background(), target, AttachmentForTest(imagePath, "image/png", "photo.png"), "图片"); err != nil {
		t.Fatalf("SendAttachment(image) error = %v", err)
	}
	if err := sender.SendAttachment(context.Background(), target, AttachmentForTest(filePath, "application/pdf", "report.pdf"), "文件"); err != nil {
		t.Fatalf("SendAttachment(file) error = %v", err)
	}
	if len(uploadMediaTypes) != 2 || uploadMediaTypes[0] != UploadMediaTypeImage || uploadMediaTypes[1] != UploadMediaTypeFile {
		t.Fatalf("upload media types = %#v, want image then file", uploadMediaTypes)
	}
	if len(sentItemTypes) != 2 || sentItemTypes[0] != ItemImage || sentItemTypes[1] != ItemFile {
		t.Fatalf("sent item types = %#v, want image then file", sentItemTypes)
	}
	decodedFileAESKey, err := base64.StdEncoding.DecodeString(sentFileAESKey)
	if err != nil {
		t.Fatalf("decode file aes key: %v", err)
	}
	if len(decodedFileAESKey) != aes.BlockSize {
		t.Fatalf("file aes key decoded length = %d, want raw AES key length %d", len(decodedFileAESKey), aes.BlockSize)
	}
}

func TestUploadMediaReturnsGetUploadURLErrCode(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(filePath, []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewClient(ClientConfig{
		BaseURL: "https://wechat.test",
		Token:   "token",
		HTTPDo: func(req *http.Request) (*http.Response, error) {
			if req.URL.Host+req.URL.Path != "wechat.test/ilink/bot/getuploadurl" {
				t.Fatalf("unexpected request URL = %s", req.URL.String())
			}
			return jsonHTTPResponse(http.StatusOK, map[string]any{
				"errcode": -14,
				"errmsg": "session timeout",
			}), nil
		},
	})

	_, err := c.UploadMedia(context.Background(), filePath, "user-1", UploadMediaTypeFile)
	if err == nil {
		t.Fatal("UploadMedia() error = nil, want getuploadurl errcode")
	}
	if !strings.Contains(err.Error(), "errcode=-14") || !strings.Contains(err.Error(), "session timeout") {
		t.Fatalf("UploadMedia() error = %v, want session timeout errcode", err)
	}
	if strings.Contains(err.Error(), "missing upload URL") {
		t.Fatalf("UploadMedia() error = %v, should not mask errcode as missing upload URL", err)
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

func jsonHTTPResponse(status int, body any) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(raw)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func SendTargetForTest(userID, contextToken string) agentchannel.SendTarget {
	return agentchannel.SendTarget{
		Channel:      agentchannel.TypeWechat,
		UserID:       userID,
		ContextToken: contextToken,
	}
}

func AttachmentForTest(path, mimeType, displayName string) agentchannel.Attachment {
	return agentchannel.Attachment{
		Path:        path,
		MIMEType:    mimeType,
		DisplayName: displayName,
	}
}
