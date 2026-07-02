package provider

import (
	"context"
	"strings"
	"testing"
)

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		id      string
		baseURL string
		want    string
	}{
		{id: "openrouter:qwen", want: "openrouter"},
		{id: "openrouter", want: "openrouter"},
		{baseURL: "https://api.siliconflow.cn/v1", want: "siliconflow"},
		{baseURL: "https://ark.cn-beijing.volces.com/api/v3", want: "ark"},
		{baseURL: "https://integrate.api.nvidia.com/v1", want: "nvidia"},
		{id: "custom", baseURL: "https://example.test/v1", want: ""},
	}
	for _, tt := range tests {
		if got := DetectProvider(tt.id, tt.baseURL); got != tt.want {
			t.Fatalf("DetectProvider(%q, %q) = %q, want %q", tt.id, tt.baseURL, got, tt.want)
		}
	}
}

func TestUsageFromModelsMissingKey(t *testing.T) {
	got := UsageFromModels(context.Background(), []ModelConfig{{ID: "openrouter", BaseURL: "https://openrouter.ai/api/v1"}})
	if got["OpenRouter"] != "未配置 API Key" {
		t.Fatalf("OpenRouter usage = %q, want missing key", got["OpenRouter"])
	}
}

func TestParseOpenRouterKeyUsage(t *testing.T) {
	got, err := parseOpenRouterKeyUsage([]byte(`{"data":{"usage":25.5,"limit":100,"limit_remaining":74.5,"limit_reset":"monthly"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"$25.50 used", "$100.00 limit", "$74.50 remaining", "monthly"} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage = %q, want %q", got, want)
		}
	}
}
