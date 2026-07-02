package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type OpenRouter struct{}

func (OpenRouter) Name() string { return "OpenRouter" }

func (OpenRouter) CheckBalance(ctx context.Context, apiKey string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/auth/key", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		Data *struct {
			Label  string  `json:"label"`
			Credit float64 `json:"credit"`
			Usage  float64 `json:"usage"`
			Limit  float64 `json:"limit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("unmarshal: %w", err)
	}
	if result.Data == nil {
		return "", fmt.Errorf("response missing data field")
	}

	return fmt.Sprintf("$%.2f ($%.2f used)", result.Data.Credit, result.Data.Usage), nil
}
