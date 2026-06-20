package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mnhkahn/gogogo/logger"
)

type modelLimitEntry struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

type modelsDevModel struct {
	ID    string          `json:"id"`
	Limit modelLimitEntry `json:"limit"`
}

type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

func refreshModelLimitsOnce(configs map[string]LLMModelConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	remote := fetchModelLimits(ctx)
	if remote == nil {
		return
	}
	mergeModelLimits(configs, remote)
}

func fetchModelLimits(ctx context.Context) map[string]modelLimitEntry {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://models.dev/api.json", nil)
	if err != nil {
		logger.Infof("[model_limits] request creation failed: %v", err)
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Infof("[model_limits] fetch failed: %v", err)
		return nil
	}
	defer resp.Body.Close()

	var providers map[string]modelsDevProvider
	if err := json.NewDecoder(resp.Body).Decode(&providers); err != nil {
		logger.Infof("[model_limits] decode failed: %v", err)
		return nil
	}

	result := make(map[string]modelLimitEntry)
	for _, p := range providers {
		for mid, m := range p.Models {
			entry := result[mid]
			if m.Limit.Context > entry.Context {
				entry.Context = m.Limit.Context
			}
			if m.Limit.Output > entry.Output {
				entry.Output = m.Limit.Output
			}
			result[mid] = entry
		}
	}
	logger.Infof("[model_limits] loaded %d model limits", len(result))
	return result
}

func mergeModelLimits(configs map[string]LLMModelConfig, remote map[string]modelLimitEntry) {
	if remote == nil {
		return
	}
	for id, cfg := range configs {
		entry, ok := remote[cfg.Model]
		if !ok {
			continue
		}
		changed := false
		if cfg.ContextLength <= 0 && entry.Context > 0 {
			cfg.ContextLength = entry.Context
			changed = true
		}
		if cfg.MaxTokens <= 0 && entry.Output > 0 {
			cfg.MaxTokens = entry.Output
			changed = true
		}
		if changed {
			configs[id] = cfg
		}
	}
}
