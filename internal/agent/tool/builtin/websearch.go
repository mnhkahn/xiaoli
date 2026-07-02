package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const (
	anySearchURL        = "https://api.anysearch.com/mcp"
	anySearchTimeout    = 25 * time.Second
	defaultMaxResults   = 8
	anySearchMaxResults = 10
)

type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type WebSearchResponse struct {
	Query   string            `json:"query"`
	Results []WebSearchResult `json:"results,omitempty"`
	Error   string            `json:"error,omitempty"`
}

type WebSearchTool struct {
	client *http.Client
	apiKey string
}

func NewWebSearchTool(apiKey string) *WebSearchTool {
	return &WebSearchTool{
		client: &http.Client{Timeout: anySearchTimeout},
		apiKey: apiKey,
	}
}

func (t *WebSearchTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "websearch",
		Desc: "Search the web for current information. Use this for real-time data, recent events, or anything beyond the model's knowledge cutoff. Results include titles, URLs, and summaries.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {
				Type:     schema.String,
				Desc:     "Web search query",
				Required: true,
			},
			"count": {
				Type: schema.Integer,
				Desc: "Number of search results to return (default 8, max 10)",
			},
		}),
	}, nil
}

type anySearchRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type anySearchCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type anySearchResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *anySearchError `json:"error,omitempty"`
}

type anySearchError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type anySearchContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anySearchResult struct {
	Content []anySearchContentItem `json:"content"`
}

func (t *WebSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return encodeSearchResponse(WebSearchResponse{Error: "invalid JSON arguments"}), nil
	}
	args.Query = trimQuery(args.Query)
	if args.Query == "" {
		return encodeSearchResponse(WebSearchResponse{Error: "query is required"}), nil
	}
	count := args.Count
	if count <= 0 {
		count = defaultMaxResults
	}
	if count > anySearchMaxResults {
		count = anySearchMaxResults
	}

	callArgs, _ := json.Marshal(map[string]any{
		"query":       args.Query,
		"max_results": count,
	})
	params, _ := json.Marshal(anySearchCallParams{
		Name:      "search",
		Arguments: callArgs,
	})
	body, _ := json.Marshal(anySearchRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  params,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anySearchURL, bytes.NewReader(body))
	if err != nil {
		return encodeSearchResponse(WebSearchResponse{Error: err.Error()}), nil
	}
	req.Header.Set("Content-Type", "application/json")
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return encodeSearchResponse(WebSearchResponse{Error: err.Error()}), nil
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return encodeSearchResponse(WebSearchResponse{Error: err.Error()}), nil
	}

	var rpcResp anySearchResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return encodeSearchResponse(WebSearchResponse{Error: fmt.Sprintf("parse response: %v", err)}), nil
	}
	if rpcResp.Error != nil {
		return encodeSearchResponse(WebSearchResponse{Error: fmt.Sprintf("API error: %s", rpcResp.Error.Message)}), nil
	}

	var result anySearchResult
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return encodeSearchResponse(WebSearchResponse{Error: fmt.Sprintf("parse result: %v", err)}), nil
	}

	webResp := WebSearchResponse{Query: args.Query}
	for _, item := range result.Content {
		if item.Type == "text" && item.Text != "" {
			webResp.Results = parseSearchResults(item.Text)
		}
	}

	return encodeSearchResponse(webResp), nil
}

func parseSearchResults(text string) []WebSearchResult {
	return parseAnySearchText(text)
}

func trimQuery(q string) string {
	var out []byte
	for _, b := range []byte(q) {
		if b >= 32 {
			out = append(out, b)
		}
	}
	return string(out)
}

func encodeSearchResponse(resp WebSearchResponse) string {
	raw, _ := json.Marshal(resp)
	return string(raw)
}

func parseAnySearchText(text string) []WebSearchResult {
	var results []WebSearchResult
	lines := splitLines(text)
	var current struct {
		title string
		url   string
		body  string
	}
	flush := func() {
		if current.title != "" || current.url != "" {
			results = append(results, WebSearchResult{
				Title:   current.title,
				URL:     current.url,
				Content: current.body,
			})
			current.title = ""
			current.url = ""
			current.body = ""
		}
	}
	for _, line := range lines {
		line = trimRight(line)
		if line == "" {
			flush()
			continue
		}
		if startsWithNum(line) {
			flush()
			current.title = line
		} else if hasURL(line) {
			current.url = extractURL(line)
		} else {
			if current.body != "" {
				current.body += " "
			}
			current.body += line
		}
	}
	flush()
	return results
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimRight(s string) string {
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func startsWithNum(s string) bool {
	if s == "" {
		return false
	}
	return s[0] >= '0' && s[0] <= '9'
}

func hasURL(s string) bool {
	idx := indexOfStr(s, "http")
	return idx >= 0
}

func extractURL(s string) string {
	idx := indexOfStr(s, "http")
	if idx < 0 {
		return ""
	}
	end := idx
	for end < len(s) && s[end] != ' ' && s[end] != '\t' {
		end++
	}
	return s[idx:end]
}

func indexOfStr(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
