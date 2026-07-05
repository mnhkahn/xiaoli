package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	htmlstd "html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const (
	defaultTimeout = 30 * time.Second
	maxTimeout     = 120 * time.Second
	defaultMaxSize = 5 * 1024 * 1024
)

type Config struct {
	HTTPClient *http.Client
	MaxBytes   int64
}

type Response struct {
	URL         string `json:"url,omitempty"`
	StatusCode  int    `json:"status_code,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Format      string `json:"format,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
	Content     string `json:"content,omitempty"`
	Error       string `json:"error,omitempty"`
}

type WebFetchTool struct {
	client   *http.Client
	maxBytes int64
}

func NewWebFetchTool(cfg Config) *WebFetchTool {
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxSize
	}
	return &WebFetchTool{client: client, maxBytes: maxBytes}
}

func NewTools(webFetchEnabled bool) []tool.BaseTool {
	if !webFetchEnabled {
		return nil
	}
	return []tool.BaseTool{NewWebFetchTool(Config{})}
}

func NewFilteredTools(filters ToolFilter, opts ToolOptions) []tool.BaseTool {
	var tools []tool.BaseTool
	if filters&ToolWebFetch != 0 {
		tools = append(tools, NewWebFetchTool(Config{}))
	}
	if filters&ToolWebSearch != 0 {
		tools = append(tools, NewWebSearchTool(opts.WebSearchKey))
	}
	if filters&ToolAskUserQuestion != 0 {
		tools = append(tools, NewAskUserQuestionTool())
	}
	if filters&ToolMemorySave != 0 && opts.MemoryBackends != nil {
		tools = append(tools, NewMemorySaveTool(opts.MemoryBackends))
	}
	if filters&ToolMemoryForget != 0 && opts.MemoryBackends != nil {
		tools = append(tools, NewMemoryForgetTool(opts.MemoryBackends))
	}
	if filters&ToolMemoryList != 0 && opts.MemoryBackends != nil {
		tools = append(tools, NewMemoryListTool(opts.MemoryBackends))
	}
	if filters&ToolBash != 0 && opts.ShellConfig != nil {
		tools = append(tools, NewShellTool(*opts.ShellConfig))
	}
	if filters&ToolReminder != 0 && opts.ReminderStore != nil {
		tools = append(tools,
			NewReminderAddTool(opts.ReminderStore, opts.Timezone),
			NewReminderListTool(opts.ReminderStore),
			NewReminderDeleteTool(opts.ReminderStore),
		)
	}
	if filters&ToolLog != 0 && opts.LogDir != "" {
		tools = append(tools, NewLogSearchTool(opts.LogDir))
	}
	if filters&ToolInspectRecentImage != 0 && inspectRecentImageToolAvailable(opts.VisionAnalyzer, opts.RecentImages) {
		tools = append(tools, NewInspectRecentImageTool(opts.VisionAnalyzer, opts.RecentImages))
	}
	if filters&ToolFileWrite != 0 && len(opts.FileWriteRoots) > 0 {
		tools = append(tools, NewFileWriteTool(FileWriteConfig{AllowedRoots: opts.FileWriteRoots}))
	}
	if filters&ToolCodeFiles != 0 && len(opts.FileRoots) > 0 {
		cfg := FileToolConfig{AllowedRoots: opts.FileRoots}
		tools = append(tools,
			NewGlobTool(cfg),
			NewReadFileTool(cfg),
			NewGrepTool(cfg),
			NewEditFileTool(cfg),
		)
	}
	return tools
}

func (t *WebFetchTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "webfetch",
		Desc: "Fetch a public http/https web page with GET and return cleaned markdown, text, or html. Use it when the user asks to read a URL or needs current webpage content. It does not send cookies, credentials, or request bodies.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"url": {
				Type:     schema.String,
				Desc:     "Public http or https URL to fetch. Private/local network hosts and embedded credentials are rejected.",
				Required: true,
			},
			"format": {
				Type: schema.String,
				Desc: "Output format: markdown, text, or html. Defaults to markdown.",
				Enum: []string{"markdown", "text", "html"},
			},
			"timeout": {
				Type: schema.Integer,
				Desc: "Timeout in seconds. Defaults to 30 and is capped at 120.",
			},
		}),
	}, nil
}

func (t *WebFetchTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		URL     string `json:"url"`
		Format  string `json:"format"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return encodeResponse(Response{Error: "invalid JSON arguments"}), nil
	}
	args.Format = normalizeFormat(args.Format)
	parsed, err := validatePublicURL(args.URL)
	if err != nil {
		return encodeResponse(Response{Error: err.Error()}), nil
	}

	timeout := defaultTimeout
	if args.Timeout > 0 {
		timeout = time.Duration(args.Timeout) * time.Second
		if timeout > maxTimeout {
			timeout = maxTimeout
		}
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return encodeResponse(Response{Error: err.Error()}), nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; XiaoliWebFetch/1.0; +https://xiaoli-server.local)")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Accept", acceptHeader(args.Format))

	resp, err := t.client.Do(req)
	if err != nil {
		return encodeResponse(Response{Error: err.Error()}), nil
	}
	defer resp.Body.Close()

	if resp.ContentLength > t.maxBytes {
		return encodeResponse(Response{URL: resp.Request.URL.String(), StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Format: args.Format, Error: fmt.Sprintf("response exceeds max size %d bytes", t.maxBytes)}), nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, t.maxBytes+1))
	if err != nil {
		return encodeResponse(Response{Error: err.Error()}), nil
	}
	if int64(len(raw)) > t.maxBytes {
		return encodeResponse(Response{URL: resp.Request.URL.String(), StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Format: args.Format, Error: fmt.Sprintf("response exceeds max size %d bytes", t.maxBytes)}), nil
	}

	contentType := resp.Header.Get("Content-Type")
	content := string(raw)
	if args.Format != "html" && strings.Contains(strings.ToLower(contentType), "html") {
		if args.Format == "text" {
			content = htmlToText(content)
		} else {
			content = htmlToMarkdown(content)
		}
	}

	return encodeResponse(Response{
		URL:         resp.Request.URL.String(),
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		Format:      args.Format,
		Bytes:       len(raw),
		Content:     content,
	}), nil
}

func encodeResponse(resp Response) string {
	raw, _ := json.Marshal(resp)
	return string(raw)
}

func normalizeFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "text", "html":
		return strings.ToLower(strings.TrimSpace(format))
	default:
		return "markdown"
	}
}

func acceptHeader(format string) string {
	switch format {
	case "html":
		return "text/html,application/xhtml+xml,text/plain;q=0.8,*/*;q=0.5"
	case "text":
		return "text/plain,text/html;q=0.8,*/*;q=0.5"
	default:
		return "text/markdown,text/html,application/xhtml+xml,text/plain;q=0.8,*/*;q=0.5"
	}
}

func validatePublicURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2000 {
		return nil, fmt.Errorf("url is too long")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("only http and https URLs are allowed")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("URLs with embedded credentials are not allowed")
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, fmt.Errorf("invalid host")
	}
	if isPrivateHost(host) {
		return nil, fmt.Errorf("private or local hosts are not allowed")
	}
	return parsed, nil
}

func isPrivateHost(host string) bool {
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || strings.HasSuffix(lower, ".local") {
		return true
	}
	if ip := net.ParseIP(lower); ip != nil {
		return !isPublicIP(ip)
	}
	return false
}

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}

func htmlToText(input string) string {
	return normalizeWhitespace(tagsToText(stripSkippedHTML(input)))
}

func htmlToMarkdown(input string) string {
	input = stripSkippedHTML(input)
	var b strings.Builder
	for len(input) > 0 {
		tagStart := strings.IndexByte(input, '<')
		if tagStart < 0 {
			writeWord(&b, htmlstd.UnescapeString(input))
			break
		}
		writeWord(&b, htmlstd.UnescapeString(input[:tagStart]))
		input = input[tagStart:]
		tagEnd := strings.IndexByte(input, '>')
		if tagEnd < 0 {
			break
		}
		tag, closing := parseTag(input[1:tagEnd])
		switch {
		case !closing && headingLevel(tag) > 0:
			ensureBlankLine(&b)
			b.WriteString(strings.Repeat("#", headingLevel(tag)))
			b.WriteByte(' ')
		case tag == "p" || tag == "div" || tag == "section" || tag == "article" || tag == "main" || headingLevel(tag) > 0:
			ensureBlankLine(&b)
		case !closing && tag == "br":
			ensureNewLine(&b)
		case !closing && tag == "li":
			ensureNewLine(&b)
			b.WriteString("- ")
		}
		input = input[tagEnd+1:]
	}
	return strings.TrimSpace(compactBlankLines(b.String()))
}

var skippedBlockREs = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<script\b[^>]*>.*?</\s*script\s*>`),
	regexp.MustCompile(`(?is)<style\b[^>]*>.*?</\s*style\s*>`),
	regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</\s*noscript\s*>`),
	regexp.MustCompile(`(?is)<iframe\b[^>]*>.*?</\s*iframe\s*>`),
	regexp.MustCompile(`(?is)<svg\b[^>]*>.*?</\s*svg\s*>`),
}
var tagRE = regexp.MustCompile(`(?s)<[^>]+>`)

func stripSkippedHTML(input string) string {
	for _, re := range skippedBlockREs {
		input = re.ReplaceAllString(input, " ")
	}
	return input
}

func tagsToText(input string) string {
	return htmlstd.UnescapeString(tagRE.ReplaceAllString(input, " "))
}

func parseTag(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	closing := strings.HasPrefix(raw, "/")
	raw = strings.TrimPrefix(raw, "/")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", closing
	}
	for i, r := range raw {
		if unicode.IsSpace(r) || r == '/' {
			return strings.ToLower(raw[:i]), closing
		}
	}
	return strings.ToLower(raw), closing
}

func headingLevel(tag string) int {
	if len(tag) == 2 && tag[0] == 'h' {
		n, err := strconv.Atoi(tag[1:])
		if err == nil && n >= 1 && n <= 6 {
			return n
		}
	}
	return 0
}

func isBlock(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "div", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "table", "ul":
		return true
	default:
		return false
	}
}

func writeWord(b *strings.Builder, text string) {
	text = normalizeWhitespace(text)
	if text == "" {
		return
	}
	if b.Len() > 0 {
		last := b.String()[b.Len()-1]
		if last != ' ' && last != '\n' && !startsWithPunctuation(text) {
			b.WriteByte(' ')
		}
	}
	b.WriteString(text)
}

func startsWithPunctuation(text string) bool {
	if text == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(text)
	return strings.ContainsRune(".,;:!?)]}，。！？；：、）】》", r)
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

func ensureNewLine(b *strings.Builder) {
	if b.Len() == 0 {
		return
	}
	if b.String()[b.Len()-1] != '\n' {
		b.WriteByte('\n')
	}
}

func ensureBlankLine(b *strings.Builder) {
	if b.Len() == 0 {
		return
	}
	s := b.String()
	switch {
	case strings.HasSuffix(s, "\n\n"):
		return
	case strings.HasSuffix(s, "\n"):
		b.WriteByte('\n')
	default:
		b.WriteString("\n\n")
	}
}

func compactBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}
