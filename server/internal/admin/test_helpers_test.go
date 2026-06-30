package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, payload any) *http.Response {
	raw, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(raw))),
	}
}

func testConfig() Config {
	cfg := Config{
		PublicBaseURL:           "https://example.test",
		SessionSecret:           "test-secret",
		SessionMaxAge:           7 * 24 * time.Hour,
		GoLLMTimeout:            time.Second,
		GoVLLMTimeout:           time.Second,
		GoASRTimeout:            time.Second,
		GoTTSTimeout:            time.Second,
		SkillMaxBytes:           1024 * 1024,
		SkillExecTimeout:        time.Second,
		SkillExecMaxOutputBytes: 1024 * 1024,
		Now: func() time.Time {
			return time.Date(2026, 6, 30, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
		},
	}
	return cfg
}
