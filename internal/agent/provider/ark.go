package provider

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type ARK struct{}

func (ARK) Name() string { return "ARK" }

func (ARK) CheckBalance(ctx context.Context, apiKey string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ark.cn-beijing.volces.com/api/v1/balance", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("查询失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return "有可用额度", nil
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return "N/A（需单独授权）", nil
	}
	return fmt.Sprintf("状态码 %d", resp.StatusCode), nil
}
