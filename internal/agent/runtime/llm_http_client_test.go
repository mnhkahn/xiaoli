package runtime

import (
	"io"
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

	_, err := newLLMHTTPClient(time.Second, 20*time.Millisecond, nil).Get(server.URL)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "timeout awaiting response headers") {
		t.Fatalf("header timeout error = %v", err)
	}
	if !isTransientTimeoutMessage(strings.ToLower(err.Error())) {
		t.Fatalf("header timeout was not retryable: %v", err)
	}
}

func TestLLMHTTPClientCapturesConcreteResponseModel(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "json", body: `{"id":"chatcmpl_1","model":"poolside/laguna-xs-2.1:free","choices":[]}`},
		{name: "sse", body: "data: {\"id\":\"chatcmpl_1\",\"model\":\"poolside/laguna-xs-2.1:free\",\"choices\":[]}\n\ndata: [DONE]\n\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			var got string
			resp, err := newLLMHTTPClient(time.Second, time.Second, func(model string) { got = model }).Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if got != "poolside/laguna-xs-2.1:free" {
				t.Fatalf("captured model = %q", got)
			}
		})
	}
}
