package runtime

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
)

const (
	llmRetryMax      = 2
	llmBackoffBase   = 500 * time.Millisecond
	llmBackoffCap    = 10 * time.Second
	agentRetryMax    = 1
	agentBackoffBase = 2 * time.Second
	agentBackoffCap  = 30 * time.Second
)

var rateLimitQuotaPhrases = []string{
	"insufficient_quota",
	"quota_exceeded",
	"quota insufficient",
	"out of quota",
	"exceeded your current quota",
}

var rateLimitGenericPhrases = []string{
	"rate limit",
	"rate_limit",
	"too many requests",
	"too_many_requests",
	"exhausted",
	"retry after",
	"retry_after",
}

var transientTimeoutPhrases = []string{
	"context deadline exceeded",
	"client.timeout exceeded",
	"i/o timeout",
	"timeout awaiting headers",
}

func llmShouldRetry(ctx context.Context, retryCtx *adk.RetryContext) *adk.RetryDecision {
	if retryCtx.Err == nil {
		return nil
	}

	isQuota := false

	var apiErr *openai.APIError
	if errors.As(retryCtx.Err, &apiErr) {
		if apiErr.HTTPStatusCode == 429 || apiErr.HTTPStatusCode == 0 {
			msg := strings.ToLower(apiErr.Message)
			for _, p := range rateLimitQuotaPhrases {
				if strings.Contains(msg, p) {
					isQuota = true
					break
				}
			}
			if !isQuota {
				for _, p := range rateLimitGenericPhrases {
					if strings.Contains(msg, p) {
						return &adk.RetryDecision{Retry: true}
					}
				}
			}
		} else if apiErr.HTTPStatusCode >= 500 {
			return &adk.RetryDecision{Retry: true}
		}
	} else {
		msg := strings.ToLower(retryCtx.Err.Error())
		if isTransientTimeoutMessage(msg) {
			return &adk.RetryDecision{Retry: true}
		}
		for _, p := range rateLimitGenericPhrases {
			if strings.Contains(msg, p) {
				return &adk.RetryDecision{Retry: true}
			}
		}
	}

	if isQuota {
		return &adk.RetryDecision{
			Retry:        false,
			RewriteError: &quotaExceededError{msg: retryCtx.Err.Error()},
		}
	}

	return nil
}

func isTransientTimeoutMessage(msg string) bool {
	if strings.Contains(msg, "context canceled") {
		return false
	}
	for _, p := range transientTimeoutPhrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

type quotaExceededError struct {
	msg string
}

func (e *quotaExceededError) Error() string {
	return "quota exceeded: " + e.msg
}

func llmBackoffFunc(ctx context.Context, attempt int) time.Duration {
	d := llmBackoffBase * (1 << (attempt - 1))
	if d > llmBackoffCap {
		d = llmBackoffCap
	}
	jitter := float64(rand.Intn(41)+80) / 100.0
	return time.Duration(float64(d) * jitter)
}

func newLLMRetryConfig() *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries:  llmRetryMax,
		ShouldRetry: llmShouldRetry,
		BackoffFunc: llmBackoffFunc,
	}
}

func isQuotaError(err error) bool {
	var qe *quotaExceededError
	return errors.As(err, &qe)
}

func isRetryableAgentError(err error) bool {
	if err == nil {
		return false
	}

	if isQuotaError(err) {
		return false
	}

	msg := strings.ToLower(err.Error())
	if isTransientTimeoutMessage(msg) {
		return true
	}
	for _, p := range rateLimitGenericPhrases {
		if strings.Contains(msg, p) {
			return true
		}
	}

	for _, p := range rateLimitQuotaPhrases {
		if strings.Contains(msg, p) {
			return false
		}
	}

	if strings.Contains(msg, "5") || strings.Contains(msg, "internal error") || strings.Contains(msg, "server error") || strings.Contains(msg, "service unavailable") || strings.Contains(msg, "bad gateway") || strings.Contains(msg, "gateway timeout") {
		return true
	}

	return false
}

func agentRetryBackoff(attempt int) time.Duration {
	d := agentBackoffBase * (1 << (attempt - 1))
	if d > agentBackoffCap {
		d = agentBackoffCap
	}
	jitter := float64(rand.Intn(41)+80) / 100.0
	return time.Duration(float64(d) * jitter)
}

func runWithRetry(ctx context.Context, runner *adk.Runner, msgs []*schema.Message) ([]*adk.AgentEvent, error) {
	for attempt := 0; attempt <= agentRetryMax; attempt++ {
		if st := traceFromContext(ctx); st != nil {
			logger.Infof("%s runner.start attempt=%d input_messages=%d", tracePrefix(st), attempt+1, len(msgs))
		}
		it := runner.Run(ctx, msgs)
		var events []*adk.AgentEvent
		var lastErr error
		eventIndex := 0
		for {
			event, ok := it.Next()
			if !ok {
				break
			}
			eventIndex++
			logTraceEvent(ctx, eventIndex, event)
			if event.Err != nil {
				lastErr = event.Err
				break
			}
			events = append(events, event)
		}
		if lastErr == nil {
			if st := traceFromContext(ctx); st != nil {
				logger.Infof("%s runner.end attempt=%d events=%d", tracePrefix(st), attempt+1, len(events))
			}
			return events, nil
		}
		logTraceFailure(ctx, lastErr, events)

		if attempt < agentRetryMax && isRetryableAgentError(lastErr) {
			delay := agentRetryBackoff(attempt + 1)
			logger.Infof("agent retry attempt=%d after=%v error=%v", attempt+1, delay, lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		return nil, lastErr
	}

	return nil, errors.New("agent retry exhausted")
}
