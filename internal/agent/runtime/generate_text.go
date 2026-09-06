package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// GenerateText performs one tool-free request. The caller owns its deadline and
// retry policy; non-streaming generation must not use the chat header timeout.
func (a *Agent) GenerateText(ctx context.Context, system, user string) (string, error) {
	modelID := a.CurrentLLMModel()
	model, err := a.newChatModel(ctx, modelID, 0)
	if err != nil {
		return "", fmt.Errorf("create chat model: %w", err)
	}
	msgs := []*schema.Message{schema.SystemMessage(system), schema.UserMessage(user)}
	msg, err := model.Generate(a.recorder.WithContext(ctx, modelID), msgs)
	if err != nil {
		return "", err
	}
	if msg == nil || strings.TrimSpace(msg.Content) == "" {
		return "", errors.New("model returned empty response")
	}
	return msg.Content, nil
}

// IsRetryableGenerationError excludes exhausted quotas and permanent request
// errors even when the provider reports them as HTTP 429.
func IsRetryableGenerationError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, phrase := range []string{"daily limit", "daily quota", "limit_rpd", "insufficient_quota", "quota exceeded", "quota exhausted", "insufficient credits"} {
		if strings.Contains(msg, phrase) {
			return false
		}
	}
	if status := httpStatusCode(err); status != 0 {
		return status == 408 || status == 429 || status >= 500
	}
	var netErr net.Error
	return errors.As(err, &netErr) || isTransientTimeoutMessage(msg) ||
		strings.Contains(msg, "received empty choices") ||
		strings.Contains(msg, "empty choices from openai api") ||
		strings.Contains(msg, "model returned empty response") ||
		strings.Contains(msg, "unexpected eof")
}
