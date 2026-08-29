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

type DeepSeek struct{}

func (DeepSeek) Name() string { return "DeepSeek" }

func (DeepSeek) CheckBalance(ctx context.Context, apiKey string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.deepseek.com/user/balance", nil)
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
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return parseDeepSeekBalance(body)
}

func (DeepSeek) CheckAccountBalance(ctx context.Context, apiKey string) (string, error) {
	return DeepSeek{}.CheckBalance(ctx, apiKey)
}

func parseDeepSeekBalance(body []byte) (string, error) {
	var result struct {
		BalanceInfos []struct {
			Currency     string `json:"currency"`
			TotalBalance string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("unmarshal: %w", err)
	}
	balances := make([]string, 0, len(result.BalanceInfos))
	for _, info := range result.BalanceInfos {
		currency := strings.ToUpper(strings.TrimSpace(info.Currency))
		amount := strings.TrimSpace(info.TotalBalance)
		if amount == "" {
			continue
		}
		switch currency {
		case "CNY":
			balances = append(balances, "¥"+amount)
		case "USD":
			balances = append(balances, "$"+amount)
		default:
			balances = append(balances, currency+" "+amount)
		}
	}
	if len(balances) == 0 {
		return "", fmt.Errorf("response missing balance_infos")
	}
	return strings.Join(balances, ", "), nil
}
