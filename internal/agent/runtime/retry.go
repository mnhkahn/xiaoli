package runtime

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"

	agentevent "github.com/mnhkahn/xiaoli/internal/event"
)

const (
	llmRetryMax          = 5
	llmBackoffBase       = time.Second
	llmBackoffCap        = 30 * time.Second
	llmFirstTokenTimeout = 15 * time.Second
	agentRetryMax        = 1
	agentBackoffBase     = 2 * time.Second
	agentBackoffCap      = 30 * time.Second
)

var rateLimitGenericPhrases = []string{
	"rate limit",
	"rate_limit",
	"too many requests",
	"too_many_requests",
	"exhausted",
	"retry after",
	"retry_after",
}

var httpStatusInErrorPattern = regexp.MustCompile(`(?i)\b(?:http\s+)?status(?:\s+code)?\s*[:=]?\s*(\d{3})\b`)

var transientTimeoutPhrases = []string{
	"context deadline exceeded",
	"client.timeout exceeded",
	"i/o timeout",
	"timeout awaiting headers",
	"timeout awaiting response headers",
	"first token timeout",
}

type retryReporter struct {
	eventBus  agentevent.Publisher
	sessionID string
}
type retryReporterKey struct{}

func withRetryReporter(ctx context.Context, eventBus agentevent.Publisher, sessionID string) context.Context {
	return context.WithValue(ctx, retryReporterKey{}, retryReporter{eventBus: eventBus, sessionID: sessionID})
}

func reportModelRetry(ctx context.Context, attempt int, err error) {
	r, _ := ctx.Value(retryReporterKey{}).(retryReporter)
	if r.eventBus == nil {
		return
	}
	_ = publishRunEvent(ctx, r.eventBus, agentevent.TypeAgentRetrying, r.sessionID, map[string]any{
		"retry": attempt, "max_retries": llmRetryMax,
		"message": fmt.Sprintf("Retrying model request (retry %d/%d)…", attempt, llmRetryMax),
		"error":   err.Error(),
	})
}

func llmShouldRetry(ctx context.Context, retryCtx *adk.RetryContext) *adk.RetryDecision {
	if retryCtx.Err == nil {
		return nil
	}
	if isRetryableHTTPStatus(httpStatusCode(retryCtx.Err)) {
		reportModelRetry(ctx, retryCtx.RetryAttempt, retryCtx.Err)
		return &adk.RetryDecision{Retry: true}
	}
	msg := strings.ToLower(retryCtx.Err.Error())
	if isTransientTimeoutMessage(msg) {
		reportModelRetry(ctx, retryCtx.RetryAttempt, retryCtx.Err)
		return &adk.RetryDecision{Retry: true}
	}
	for _, p := range rateLimitGenericPhrases {
		if strings.Contains(msg, p) {
			reportModelRetry(ctx, retryCtx.RetryAttempt, retryCtx.Err)
			return &adk.RetryDecision{Retry: true}
		}
	}
	return nil
}

func httpStatusCode(err error) int {
	if err == nil {
		return 0
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatusCode >= 100 && apiErr.HTTPStatusCode <= 599 {
		return apiErr.HTTPStatusCode
	}
	match := httpStatusInErrorPattern.FindStringSubmatch(err.Error())
	if len(match) != 2 {
		return 0
	}
	status, _ := strconv.Atoi(match[1])
	return status
}

func isRetryableHTTPStatus(status int) bool {
	return status >= 400 && status <= 599
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

func isRetryableAgentError(err error) bool {
	if err == nil {
		return false
	}

	if isRetryableHTTPStatus(httpStatusCode(err)) {
		return true
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

	if strings.Contains(msg, "internal error") || strings.Contains(msg, "server error") || strings.Contains(msg, "service unavailable") || strings.Contains(msg, "bad gateway") || strings.Contains(msg, "gateway timeout") {
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
