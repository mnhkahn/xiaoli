package provider

import (
	"context"
	"net/url"
	"sort"
	"strings"
)

type ModelConfig struct {
	ID      string
	BaseURL string
	APIKey  string
}

func UsageFromModels(ctx context.Context, models []ModelConfig) map[string]string {
	keys := map[string]string{}
	for _, model := range models {
		name := DetectProvider(model.ID, model.BaseURL)
		if name == "" {
			continue
		}
		existing, ok := keys[name]
		if !ok || (existing == "" && model.APIKey != "") {
			keys[name] = model.APIKey
		}
	}
	if len(keys) == 0 {
		return nil
	}
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	out := map[string]string{}
	for _, name := range names {
		apiKey := keys[name]
		checker := Get(name)
		label := name
		if checker != nil {
			label = checker.Name()
		}
		if apiKey == "" {
			out[label] = "未配置 API Key"
			continue
		}
		if checker == nil {
			out[label] = "N/A"
			continue
		}
		accountChecker, ok := checker.(AccountBalanceChecker)
		if !ok {
			out[name] = "不支持查询账户余额"
			continue
		}
		balance, err := accountChecker.CheckAccountBalance(ctx, apiKey)
		if err != nil {
			out[label] = "查询失败"
			continue
		}
		out[label] = balance
	}
	return out
}

// UsageForProviders queries only the named providers, in the requested scope.
// Map keys are stable provider IDs rather than display names so callers can
// control presentation independently of each provider's branding.
func UsageForProviders(ctx context.Context, apiKeys map[string]string, names ...string) map[string]string {
	out := make(map[string]string, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		checker := Get(name)
		if checker == nil {
			continue
		}
		apiKey := strings.TrimSpace(apiKeys[name])
		if apiKey == "" {
			out[name] = "未配置 API Key"
			continue
		}
		balance, err := checker.CheckBalance(ctx, apiKey)
		if err != nil {
			out[name] = "查询失败"
			continue
		}
		out[name] = balance
	}
	return out
}

func DetectProvider(id, baseURL string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if idx := strings.Index(id, ":"); idx > 0 {
		id = id[:idx]
	}
	switch id {
	case "openrouter", "deepseek", "siliconflow", "ark", "nvidia":
		return id
	}
	host := strings.ToLower(strings.TrimSpace(baseURL))
	if parsed, err := url.Parse(host); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	switch {
	case strings.Contains(host, "openrouter.ai"):
		return "openrouter"
	case strings.Contains(host, "api.deepseek.com"):
		return "deepseek"
	case strings.Contains(host, "siliconflow.cn"):
		return "siliconflow"
	case strings.Contains(host, "volces.com") || strings.Contains(host, "volcengine.com") || strings.Contains(host, "ark.cn-"):
		return "ark"
	case strings.Contains(host, "integrate.api.nvidia.com") || strings.Contains(host, "nvidia.com"):
		return "nvidia"
	default:
		return ""
	}
}
