package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadImageUsesMessageResourceAPI(t *testing.T) {
	var sawResource bool
	c := NewClient(ClientConfig{
		AppID:    "app-id",
		AppToken: "app-token",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/open-apis/auth/v3/tenant_access_token/internal":
				return jsonResponse(http.StatusOK, map[string]any{"code": 0, "tenant_access_token": "tenant-token"}), nil
			case "/open-apis/im/v1/messages/om_1/resources/img_1":
				sawResource = true
				if req.URL.Query().Get("type") != "image" {
					t.Fatalf("query type = %q, want image", req.URL.Query().Get("type"))
				}
				if got := req.Header.Get("Authorization"); got != "Bearer tenant-token" {
					t.Fatalf("Authorization = %q, want bearer token", got)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"image/png"}},
					Body:       io.NopCloser(strings.NewReader("png-bytes")),
				}, nil
			default:
				t.Fatalf("unexpected path = %s", req.URL.Path)
				return nil, nil
			}
		})},
	})

	contentType, body, err := c.DownloadImage(context.Background(), "om_1", "img_1")
	if err != nil {
		t.Fatalf("DownloadImage() error = %v", err)
	}
	if !sawResource {
		t.Fatal("resource endpoint was not called")
	}
	if contentType != "image/png" || string(body) != "png-bytes" {
		t.Fatalf("contentType/body = %q/%q, want image/png bytes", contentType, string(body))
	}
}

func TestReplyPostSendsLarkPostMessage(t *testing.T) {
	var reply map[string]any
	c := NewClient(ClientConfig{
		AppID:    "app-id",
		AppToken: "app-token",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/open-apis/auth/v3/tenant_access_token/internal":
				return jsonResponse(http.StatusOK, map[string]any{"code": 0, "tenant_access_token": "tenant-token"}), nil
			case "/open-apis/im/v1/messages/om_1/reply":
				if got := req.Header.Get("Authorization"); got != "Bearer tenant-token" {
					t.Fatalf("Authorization = %q, want bearer token", got)
				}
				if err := json.NewDecoder(req.Body).Decode(&reply); err != nil {
					t.Fatalf("decode reply body: %v", err)
				}
				return jsonResponse(http.StatusOK, map[string]any{"code": 0}), nil
			default:
				t.Fatalf("unexpected path = %s", req.URL.Path)
				return nil, nil
			}
		})},
	})

	if err := c.ReplyPost(context.Background(), "om_1", "技能列表", "可用 Skills：\n- architect：架构咨询\n- cnote：云端笔记"); err != nil {
		t.Fatalf("ReplyPost() error = %v", err)
	}
	if reply["msg_type"] != "post" {
		t.Fatalf("msg_type = %#v, want post", reply["msg_type"])
	}
	contentRaw, _ := reply["content"].(string)
	if strings.Contains(contentRaw, "- architect") {
		t.Fatalf("content = %s, should be structured post instead of raw markdown", contentRaw)
	}
	if !strings.Contains(contentRaw, `"tag":"text"`) || !strings.Contains(contentRaw, `architect`) {
		t.Fatalf("content = %s, want post text items", contentRaw)
	}
	var parsed struct {
		ZhCN struct {
			Title string `json:"title"`
		} `json:"zh_cn"`
	}
	if err := json.Unmarshal([]byte(contentRaw), &parsed); err != nil {
		t.Fatalf("content is not valid post JSON: %v", err)
	}
	if parsed.ZhCN.Title != "技能列表" {
		t.Fatalf("title = %q, want 技能列表", parsed.ZhCN.Title)
	}
}

func TestUploadImageUsesLarkImageAPI(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(imagePath, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	var sawUpload bool
	c := NewClient(ClientConfig{
		AppID:    "app-id",
		AppToken: "app-token",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/open-apis/auth/v3/tenant_access_token/internal":
				return jsonResponse(http.StatusOK, map[string]any{"code": 0, "tenant_access_token": "tenant-token"}), nil
			case "/open-apis/im/v1/images":
				sawUpload = true
				if got := req.Header.Get("Authorization"); got != "Bearer tenant-token" {
					t.Fatalf("Authorization = %q, want bearer token", got)
				}
				if err := req.ParseMultipartForm(1024 * 1024); err != nil {
					t.Fatalf("ParseMultipartForm() error = %v", err)
				}
				if got := req.FormValue("image_type"); got != "message" {
					t.Fatalf("image_type = %q, want message", got)
				}
				files := req.MultipartForm.File["image"]
				if len(files) != 1 || files[0].Filename != "photo.png" {
					t.Fatalf("image files = %#v, want photo.png", files)
				}
				return jsonResponse(http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"image_key": "img_key"}}), nil
			default:
				t.Fatalf("unexpected path = %s", req.URL.Path)
				return nil, nil
			}
		})},
	})

	key, err := c.UploadImage(context.Background(), imagePath)
	if err != nil {
		t.Fatalf("UploadImage() error = %v", err)
	}
	if !sawUpload {
		t.Fatal("image upload endpoint was not called")
	}
	if key != "img_key" {
		t.Fatalf("key = %q, want img_key", key)
	}
}

func TestParseInlineHandlesEdgeCases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"这是 **未闭合加粗", "这是 **未闭合加粗"},
		{"这是 **闭合加粗** 好了", "这是 闭合加粗 好了"},
		{"看这个 [链接", "看这个 [链接"},
		{"[正常链接](https://x.com) 文本", "正常链接(https://x.com) 文本"},
		{"开头加粗**中间有**不完整**", "开头加粗中间有不完整**"},
		{"空**加粗**结尾", "空加粗结尾"},
	}
	for _, tt := range tests {
		items := parseInline(tt.input)
		var got string
		for _, item := range items {
			if text, ok := item["text"]; ok {
				got += text
			}
			if href, ok := item["href"]; ok {
				got += "(" + href + ")"
			}
		}
		if got != tt.want {
			t.Errorf("parseInline(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, payload any) *http.Response {
	raw, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}
}
