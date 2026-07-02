package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenRouter struct{}

func (OpenRouter) Name() string { return "OpenRouter" }

func (OpenRouter) CheckBalance(ctx context.Context, apiKey string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/key", nil)
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

	return parseOpenRouterKeyUsage(body)
}

func parseOpenRouterKeyUsage(body []byte) (string, error) {
	var result struct {
		Data *struct {
			Usage          float64 `json:"usage"`
			Limit          float64 `json:"limit"`
			LimitRemaining float64 `json:"limit_remaining"`
			IsFreeTier     bool    `json:"is_free_tier"`
			LimitReset     string  `json:"limit_reset"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("unmarshal: %w", err)
	}
	if result.Data == nil {
		return "", fmt.Errorf("response missing data field")
	}
	if result.Data.Limit > 0 {
		reset := strings.TrimSpace(result.Data.LimitReset)
		if reset != "" {
			reset = ", " + reset
		}
		return fmt.Sprintf("$%.2f used / $%.2f limit, $%.2f remaining%s", result.Data.Usage, result.Data.Limit, result.Data.LimitRemaining, reset), nil
	}
	tier := ""
	if result.Data.IsFreeTier {
		tier = " (free tier)"
	}
	return fmt.Sprintf("$%.2f used%s", result.Data.Usage, tier), nil
}
