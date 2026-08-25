package runtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLLMHTTPClientTimesOutWaitingForResponseHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := newLLMHTTPClient(time.Second, 20*time.Millisecond).Get(server.URL)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "timeout awaiting response headers") {
		t.Fatalf("header timeout error = %v", err)
	}
	if !isTransientTimeoutMessage(strings.ToLower(err.Error())) {
		t.Fatalf("header timeout was not retryable: %v", err)
	}
}
