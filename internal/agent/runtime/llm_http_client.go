package runtime

import (
	"net/http"
	"time"
)

const llmResponseHeaderTimeout = 15 * time.Second

// newLLMHTTPClient bounds both the total request and the time to receive the
// first HTTP response byte (the response headers). The latter catches queued
// or stalled model requests before the much longer total-request timeout.
func newLLMHTTPClient(requestTimeout, responseHeaderTimeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	return &http.Client{Timeout: requestTimeout, Transport: transport}
}
