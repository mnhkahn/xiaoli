package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const llmResponseHeaderTimeout = 15 * time.Second

// newLLMHTTPClient bounds both the total request and the time to receive the
// first HTTP response byte (the response headers). The latter catches queued
// or stalled model requests before the much longer total-request timeout.
func newLLMHTTPClient(requestTimeout, responseHeaderTimeout time.Duration, onModel func(string)) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	return &http.Client{Timeout: requestTimeout, Transport: modelCaptureTransport{base: transport, onModel: onModel}}
}

// modelCaptureTransport observes the OpenAI-compatible response body without
// changing it. OpenRouter returns the concrete routed model in its response,
// but the Eino OpenAI adapter does not expose that field to callers.
type modelCaptureTransport struct {
	base    http.RoundTripper
	onModel func(string)
}

func (t modelCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || t.onModel == nil {
		return resp, err
	}
	resp.Body = &modelCaptureBody{ReadCloser: resp.Body, onModel: t.onModel}
	return resp, nil
}

const maxModelCaptureBytes = 64 * 1024

type modelCaptureBody struct {
	io.ReadCloser
	onModel  func(string)
	buffer   []byte
	reported bool
}

func (b *modelCaptureBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.observe(p[:n])
	}
	return n, err
}

func (b *modelCaptureBody) observe(chunk []byte) {
	if b.reported || len(chunk) == 0 {
		return
	}
	b.buffer = append(b.buffer, chunk...)
	if len(b.buffer) > maxModelCaptureBytes {
		b.buffer = b.buffer[len(b.buffer)-maxModelCaptureBytes:]
	}
	if model := concreteResponseModel(b.buffer); model != "" {
		b.reported = true
		b.onModel(model)
	}
}

func concreteResponseModel(body []byte) string {
	// Non-streaming OpenAI-compatible response.
	if model := responseModelJSON(bytes.TrimSpace(body)); model != "" {
		return model
	}
	// Streaming response: each SSE data record is an independent JSON object.
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		if model := responseModelJSON(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))); model != "" {
			return model
		}
	}
	return ""
}

func responseModelJSON(body []byte) string {
	var response struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &response); err == nil {
		return strings.TrimSpace(response.Model)
	}
	// A non-streaming response can be much larger than the capture buffer. Its
	// top-level model field is at the beginning, so extract that complete field
	// even before the rest of the JSON document has arrived.
	key := []byte(`"model"`)
	idx := bytes.Index(body, key)
	if idx < 0 {
		return ""
	}
	rest := bytes.TrimSpace(body[idx+len(key):])
	if len(rest) == 0 || rest[0] != ':' {
		return ""
	}
	rest = bytes.TrimSpace(rest[1:])
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	for end := 1; end < len(rest); end++ {
		if rest[end] != '"' || rest[end-1] == '\\' {
			continue
		}
		model, err := strconv.Unquote(string(rest[:end+1]))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(model)
	}
	return ""
}
