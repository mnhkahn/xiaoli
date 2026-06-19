package builtin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWebFetchReturnsCleanMarkdownForHTML(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "Mozilla/5.0") {
			t.Fatalf("User-Agent = %q, want browser-like UA", ua)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(`<!doctype html><html><head><title>Ignored</title><style>.x{}</style><script>alert(1)</script></head><body><main><h1>Hello</h1><p>Useful <strong>content</strong>.</p></main></body></html>`)),
			Request:    r,
		}, nil
	})}

	tool := NewWebFetchTool(Config{HTTPClient: client})
	got, err := tool.InvokableRun(context.Background(), `{"url":"https://example.com/page","format":"markdown"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	var resp Response
	if err := json.Unmarshal([]byte(got), &resp); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, got)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(resp.Content, "# Hello") || !strings.Contains(resp.Content, "Useful content.") {
		t.Fatalf("Content = %q, want cleaned markdown with heading and paragraph", resp.Content)
	}
	if strings.Contains(resp.Content, "alert") || strings.Contains(resp.Content, ".x") {
		t.Fatalf("Content = %q, want script/style removed", resp.Content)
	}
}

func TestWebFetchRejectsNonHTTPAndPrivateHosts(t *testing.T) {
	tool := NewWebFetchTool(Config{})
	for _, rawURL := range []string{
		"file:///etc/passwd",
		"http://127.0.0.1/latest",
		"http://169.254.169.254/latest/meta-data",
		"http://user:pass@example.com/",
	} {
		got, err := tool.InvokableRun(context.Background(), `{"url":"`+rawURL+`"}`)
		if err != nil {
			t.Fatalf("InvokableRun(%q) error = %v", rawURL, err)
		}
		if !strings.Contains(got, `"error"`) {
			t.Fatalf("InvokableRun(%q) = %s, want JSON error", rawURL, got)
		}
	}
}

func TestWebFetchEnforcesMaxBytes(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("0123456789abcdef")),
			Request:    r,
		}, nil
	})}

	tool := NewWebFetchTool(Config{HTTPClient: client, MaxBytes: 8})
	got, err := tool.InvokableRun(context.Background(), `{"url":"https://example.com/large","format":"text"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(got, `"response exceeds max size`) {
		t.Fatalf("InvokableRun() = %s, want max size error", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
