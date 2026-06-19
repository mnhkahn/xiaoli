package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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

	if err := c.ReplyPost(context.Background(), "om_1", "可用 Skills：\n- architect：架构咨询\n- cnote：云端笔记"); err != nil {
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
