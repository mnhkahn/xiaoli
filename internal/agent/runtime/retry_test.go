package runtime

import (
	"context"
	"errors"
	"testing"

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
