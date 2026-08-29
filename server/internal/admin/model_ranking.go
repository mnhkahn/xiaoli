package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type rankedModel struct {
	Platform      string  `json:"platform"`
	Model         string  `json:"model_name"`
	ContextWindow int     `json:"context_window"`
	TextRank      int     `json:"text_rank"`
	TextELO       float64 `json:"text_elo"`
	CodeRank      int     `json:"code_rank"`
	CodeELO       float64 `json:"code_elo"`
}

type modelRankingResponse struct {
	Models []rankedModel `json:"models"`
}

func fetchTopOpenRouterFreeModel(ctx context.Context, endpoint string, timeout time.Duration) (rankedModel, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return rankedModel{}, fmt.Errorf("ranking endpoint is empty")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return rankedModel{}, err
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return rankedModel{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rankedModel{}, fmt.Errorf("ranking endpoint returned HTTP %d", resp.StatusCode)
	}
	var result modelRankingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return rankedModel{}, fmt.Errorf("decode ranking response: %w", err)
	}
	best, ok := selectTopOpenRouterFreeModel(result.Models)
	if !ok {
		return rankedModel{}, fmt.Errorf("ranking response has no ranked OpenRouter free model")
	}
	return best, nil
}

func selectTopOpenRouterFreeModel(models []rankedModel) (rankedModel, bool) {
	var best rankedModel
	found := false
	for _, candidate := range models {
		if !strings.EqualFold(strings.TrimSpace(candidate.Platform), "OpenRouter") || !strings.HasSuffix(strings.ToLower(strings.TrimSpace(candidate.Model)), ":free") || candidate.CodeRank <= 0 {
			continue
		}
		if !found || rankedModelComesFirst(candidate, best) {
			best, found = candidate, true
		}
	}
	return best, found
}

func rankedModelComesFirst(a, b rankedModel) bool {
	if a.CodeRank != b.CodeRank {
		return a.CodeRank < b.CodeRank
	}
	if a.CodeELO != b.CodeELO {
		return a.CodeELO > b.CodeELO
	}
	aTextRank, bTextRank := rankedTextRank(a.TextRank), rankedTextRank(b.TextRank)
	if aTextRank != bTextRank {
		return aTextRank < bTextRank
	}
	if a.TextELO != b.TextELO {
		return a.TextELO > b.TextELO
	}
	return strings.TrimSpace(a.Model) < strings.TrimSpace(b.Model)
}

func rankedTextRank(rank int) int {
	if rank <= 0 {
		return int(^uint(0) >> 1)
	}
	return rank
}
