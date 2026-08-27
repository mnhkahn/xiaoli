package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
)

func TestLLMShouldRetryContextDeadlineExceeded(t *testing.T) {
	decision := llmShouldRetry(context.Background(), &adk.RetryContext{
		Err: context.DeadlineExceeded,
	})

	if decision == nil || !decision.Retry {
		t.Fatalf("llmShouldRetry(context deadline exceeded) = %#v, want retry", decision)
	}
}

func TestAgentShouldRetryContextDeadlineExceeded(t *testing.T) {
	if !isRetryableAgentError(context.DeadlineExceeded) {
		t.Fatal("isRetryableAgentError(context deadline exceeded) = false, want true")
	}
}

func TestRetryDoesNotRetryContextCanceled(t *testing.T) {
	decision := llmShouldRetry(context.Background(), &adk.RetryContext{
		Err: context.Canceled,
	})

	if decision != nil && decision.Retry {
		t.Fatalf("llmShouldRetry(context canceled) = %#v, want no retry", decision)
	}
	if isRetryableAgentError(context.Canceled) {
		t.Fatal("isRetryableAgentError(context canceled) = true, want false")
	}
}

func TestAgentShouldRetryHTTPTimeoutText(t *testing.T) {
	err := errors.New(`Post "https://ark.cn-beijing.volces.com/api/coding/v3/chat/completions": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`)
	if !isRetryableAgentError(err) {
		t.Fatal("isRetryableAgentError(Client.Timeout exceeded while awaiting headers) = false, want true")
	}
}

func TestLLMShouldRetryHTTP429WithoutRateLimitText(t *testing.T) {
	decision := llmShouldRetry(context.Background(), &adk.RetryContext{RetryAttempt: 1, Err: &openai.APIError{
		HTTPStatusCode: 429,
		HTTPStatus:     "429 Too Many Requests",
		Message:        "Provider returned error",
	}})
	if decision == nil || !decision.Retry {
		t.Fatalf("llmShouldRetry(429) = %#v, want retry", decision)
	}
}

func TestLLMShouldRetryAllHTTP4xxAnd5xx(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 404, 429, 500, 503} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			decision := llmShouldRetry(context.Background(), &adk.RetryContext{RetryAttempt: 1, Err: &openai.APIError{
				HTTPStatusCode: status,
				HTTPStatus:     fmt.Sprintf("%d Provider error", status),
				Message:        "Provider returned error",
			}})
			if decision == nil || !decision.Retry {
				t.Fatalf("llmShouldRetry(%d) = %#v, want retry", status, decision)
			}
		})
	}
}

func TestWrappedHTTPStatusIsRetryable(t *testing.T) {
	err := errors.New("[NodeRunError] error, status code: 402, status: 402 Payment Required")
	if got := httpStatusCode(err); got != 402 {
		t.Fatalf("httpStatusCode() = %d, want 402", got)
	}
	if !isRetryableAgentError(err) {
		t.Fatal("wrapped 402 must be retryable")
	}
}

func TestUnrelatedDigitIsNotRetryableHTTPStatus(t *testing.T) {
	err := errors.New("invalid max_tokens: 512")
	if isRetryableAgentError(err) {
		t.Fatal("non-HTTP error containing digit 5 must not be retryable")
	}
}
