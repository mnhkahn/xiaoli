package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mnhkahn/gogogo/logger"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	a2a "github.com/mnhkahn/xiaoli/server/internal/a2a"
)

type fakeA2AAgent struct {
	mu                      sync.Mutex
	profileCalls            int
	profileStreamCalls      int
	subAgentCalls           int
	lastProfile             agentruntime.PromptProfileRequest
	profileRequests         []agentruntime.PromptProfileRequest
	lastSubAgent            string
	lastSubSessionKey       string
	profileReply            string
	profileReplies          []string
	structuredArguments     string
	structuredArgumentQueue []string
}

func (f *fakeA2AAgent) RunPromptProfile(ctx context.Context, req agentruntime.PromptProfileRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profileCalls++
	f.lastProfile = req
	f.profileRequests = append(f.profileRequests, req)
	if req.StructuredOutput != nil && (f.structuredArguments != "" || len(f.structuredArgumentQueue) > 0) {
		arguments := f.structuredArguments
		if len(f.structuredArgumentQueue) > 0 {
			idx := f.profileCalls - 1
			if idx >= len(f.structuredArgumentQueue) {
				idx = len(f.structuredArgumentQueue) - 1
			}
			arguments = f.structuredArgumentQueue[idx]
		}
		_, err := req.StructuredOutput.Capture(arguments)
		return "", err
	}
	if len(f.profileReplies) > 0 {
		idx := f.profileCalls - 1
		if idx >= len(f.profileReplies) {
			idx = len(f.profileReplies) - 1
		}
		return f.profileReplies[idx], nil
	}
	if f.profileReply != "" {
		return f.profileReply, nil
	}
	return "profile reply", nil
}

type fakeGeekNewsFetcher struct {
	batch geekNewsReply
	err   error
}

func (f fakeGeekNewsFetcher) Fetch(context.Context, string) (geekNewsReply, error) {
	return f.batch, f.err
}

func fiveSourceNews() []geekNewsItem {
	items := make([]geekNewsItem, 5)
	for i := range items {
		items[i] = geekNewsItem{Link: "https://example.com/" + strconv.Itoa(i), Title: "source " + strconv.Itoa(i), Description: strings.Repeat("原始内容", 80), CreateTime: 1719532800 + int64(i)}
	}
	return items
}

func translatedNewsResponse(index, descriptionRunes int) string {
	return `{"title":"中文 ` + strconv.Itoa(index) + `","description":"` + strings.Repeat("详", descriptionRunes) + `"}`
}

func translatedTitleResponse(index int) string {
	return `{"title":"中文标题 ` + strconv.Itoa(index) + `"}`
}

func translatedNewsBatchResponse(count, descriptionRunes int) string {
	items := make([]geekNewsBatchTranslation, count)
	for i := range items {
		items[i] = geekNewsBatchTranslation{ID: i, Title: "中文标题 " + strconv.Itoa(i), Description: strings.Repeat("详", descriptionRunes)}
	}
	data, err := json.Marshal(geekNewsBatchTranslations{Items: items})
	if err != nil {
		panic(err)
	}
	return string(data)
}

func (f *fakeA2AAgent) RunPromptProfileStream(ctx context.Context, req agentruntime.PromptProfileRequest, emit func(agentruntime.PromptProfileStreamEvent) bool) (agentruntime.PromptProfileStreamReply, error) {
	f.profileStreamCalls++
	f.lastProfile = req
	return agentruntime.PromptProfileStreamReply{Answer: "架构建议", Reasoning: "先看边界。"}, nil
}

func (f *fakeA2AAgent) RunNamedSubAgent(ctx context.Context, name string, prompt string, sessionKey string, channelName string) (string, error) {
	f.subAgentCalls++
	f.lastSubAgent = name
	f.lastSubSessionKey = sessionKey
	return "subagent reply", nil
}

func TestA2APipelineRoutesProfileRequestToPromptProfile(t *testing.T) {
	agent := &fakeA2AAgent{}
	pipeline := newA2APipeline(agent, nil)

	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel: "a2a",
		Text:    `{"profile":"encouragement","input":{"date":"6月27日周六","type":"休息日"}}`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if reply.Text != "profile reply" {
		t.Fatalf("reply = %q, want profile reply", reply.Text)
	}
	if agent.profileCalls != 1 || agent.subAgentCalls != 0 {
		t.Fatalf("calls profile=%d subagent=%d, want profile only", agent.profileCalls, agent.subAgentCalls)
	}
	if agent.lastProfile.Name != "encouragement" || agent.lastProfile.ChannelName != "a2a" || !agent.lastProfile.AllowTools {
		t.Fatalf("lastProfile = %#v, want encouragement profile with tools", agent.lastProfile)
	}
}

func TestA2APipelineRoutesGeekNewsProfileToPromptProfile(t *testing.T) {
	agent := &fakeA2AAgent{profileReply: `{"create_time":1719532800,"summary":"今日科技新闻","news":[]}`}
	pipeline := newA2APipeline(agent, nil)

	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel:        "a2a",
		ConversationID: "a2a:partner_a:cyeam_web",
		Text:           `{"profile":"geek-news","input":{"date":"2026-06-28"}}`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !json.Valid([]byte(reply.Text)) {
		t.Fatalf("reply = %q, want valid JSON", reply.Text)
	}
	if agent.profileCalls != 1 || agent.subAgentCalls != 0 {
		t.Fatalf("calls profile=%d subagent=%d, want profile only", agent.profileCalls, agent.subAgentCalls)
	}
	if agent.lastProfile.Name != "geek-news" || agent.lastProfile.ChannelName != "a2a" || !agent.lastProfile.AllowTools {
		t.Fatalf("lastProfile = %#v, want geek-news profile with tools", agent.lastProfile)
	}
	if agent.lastProfile.SessionKey != "a2a:partner_a:cyeam_web" || !agent.lastProfile.DisableHistory {
		t.Fatalf("profile session = %q disable_history=%v, want traced session with disabled history", agent.lastProfile.SessionKey, agent.lastProfile.DisableHistory)
	}
	if agent.lastProfile.UserText != `{"date":"2026-06-28"}` {
		t.Fatalf("UserText = %q, want compact date JSON", agent.lastProfile.UserText)
	}
	if agent.lastProfile.StructuredOutput == nil {
		t.Fatal("StructuredOutput = nil, want request-scoped structured output")
	}
	if agent.lastProfile.StructuredOutput.ToolName != "structured_output" {
		t.Fatalf("StructuredOutput.ToolName = %q, want structured_output", agent.lastProfile.StructuredOutput.ToolName)
	}
}

func TestA2APipelineBuildsGeekNewsDeterministicallyFromCLIItems(t *testing.T) {
	items := fiveSourceNews()
	agent := &fakeA2AAgent{structuredArgumentQueue: []string{
		translatedNewsBatchResponse(3, 300), translatedNewsBatchResponse(2, 300), `{"ids":["n0","n1","n2","n3","n4"]}`,
		translatedNewsBatchResponse(3, 300), translatedNewsBatchResponse(2, 300), `{"ids":["n0","n1","n2","n3","n4"]}`,
	}}
	aiItems := fiveSourceNews()
	pipeline := newA2APipelineWithNewsFetcher(agent, nil, fakeGeekNewsFetcher{batch: geekNewsReply{CreateTime: 1719532800, News: items, AINews: aiItems}})

	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{Channel: "a2a", ConversationID: "a2a:partner_a:cyeam_web", Text: `{"profile":"geek-news","input":{"date":"2026-06-28"}}`})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var got geekNewsReply
	if err := json.Unmarshal([]byte(reply.Text), &got); err != nil {
		t.Fatalf("Unmarshal(reply) error = %v", err)
	}
	if len(got.News) != 5 || len(got.AINews) != 5 || got.News[0].Link != items[0].Link {
		t.Fatalf("news = %#v ai_news = %#v, want all source items when ranking falls back", got.News, got.AINews)
	}
	batchCalls, rankCalls := 0, 0
	for _, request := range agent.profileRequests {
		if request.Name == "geek-news-batch-translation" {
			batchCalls++
			if request.MaxSteps != 2 {
				t.Fatalf("%s MaxSteps = %d, want 2 for structured output", request.Name, request.MaxSteps)
			}
		}
		if request.Name == "geek-news-rank" {
			rankCalls++
		}
	}
	if rankCalls != 2 {
		t.Fatalf("rank calls = %d, want one ranking request per news group", rankCalls)
	}
	if batchCalls != 4 || agent.profileCalls != 6 {
		t.Fatalf("batch calls=%d total calls=%d, want four bounded batch translations and two rankings", batchCalls, agent.profileCalls)
	}
	if got.News[0].Image != items[0].Image || got.News[0].CreateTime != items[0].CreateTime || got.News[0].SourceTitle != items[0].Title {
		t.Fatalf("item metadata changed: %#v", got.News[0])
	}
}

func TestGeekNewsBatchIndexesBoundItemCountAndInputSize(t *testing.T) {
	items := fiveSourceNews()
	got := geekNewsBatchIndexes(items)
	if len(got) != 2 || len(got[0]) != 3 || len(got[1]) != 2 {
		t.Fatalf("batches = %#v, want 3 + 2 items", got)
	}

	longItems := []geekNewsItem{
		{Title: "first", Description: strings.Repeat("x", geekNewsBatchMaxInputRunes+1)},
		{Title: "second", Description: "short"},
	}
	got = geekNewsBatchIndexes(longItems)
	if len(got) != 2 || len(got[0]) != 1 || got[0][0] != 0 || len(got[1]) != 1 || got[1][0] != 1 {
		t.Fatalf("long-item batches = %#v, want each item isolated", got)
	}
}

func TestGeekNewsTranslationInputBoundsLongSourceText(t *testing.T) {
	input := geekNewsTranslationInput(strings.Repeat("标", geekNewsBatchMaxTitleRunes+1), strings.Repeat("首", 5000)+strings.Repeat("尾", 5000))
	if len([]rune(input.Title))+len([]rune(input.Description)) > geekNewsBatchMaxInputRunes {
		t.Fatalf("input exceeds budget: title=%d description=%d", len([]rune(input.Title)), len([]rune(input.Description)))
	}
	if !strings.Contains(input.Description, "仅保留开头和结尾") || !strings.HasPrefix(input.Description, "首") || !strings.HasSuffix(input.Description, "尾") {
		t.Fatalf("description = %q, want bounded head and tail excerpt", input.Description)
	}
}

func TestProcessGeekNewsItemsTranslatesGitHubTitle(t *testing.T) {
	item := geekNewsItem{
		Link:        "https://github.com/example/project/releases/tag/v1.2.3",
		Title:       "Release v1.2.3 fixes parser regressions",
		Description: strings.Repeat("source content ", 40),
	}
	agent := &fakeA2AAgent{structuredArguments: translatedNewsBatchResponse(1, 300)}
	pipeline := newA2APipeline(agent, nil)

	processed := pipeline.processGeekNewsItems(context.Background(), a2a.ConversationTurn{Channel: "a2a", ConversationID: "a2a:test"}, a2aPromptProfileSpec{}, "news", []geekNewsItem{item})

	if got := processed[0].Title; !containsChinese(got) {
		t.Fatalf("GitHub title = %q, want Chinese translation", got)
	}
	if got := processed[0].SourceTitle; got != item.Title {
		t.Fatalf("GitHub source_title = %q, want original %q", got, item.Title)
	}
	if agent.profileCalls != 1 || agent.profileRequests[0].Name != "geek-news-batch-translation" {
		t.Fatalf("profile requests = %#v, want one batch translation", agent.profileRequests)
	}
}

func TestA2APipelineRanksNewsGroupsIndependently(t *testing.T) {
	news := fiveSourceNews()
	aiNews := fiveSourceNews()
	for i := range aiNews {
		aiNews[i].Link = "https://example.com/ai/" + strconv.Itoa(i)
	}
	agent := &fakeA2AAgent{structuredArgumentQueue: []string{
		translatedNewsBatchResponse(3, 300), translatedNewsBatchResponse(2, 300), `{"ids":["n1","n0","n2","n3","n4"]}`,
		translatedNewsBatchResponse(3, 300), translatedNewsBatchResponse(2, 300), `{"ids":["n1","n0","n2","n3","n4"]}`,
	}}
	pipeline := newA2APipelineWithNewsFetcher(agent, nil, fakeGeekNewsFetcher{batch: geekNewsReply{News: news, AINews: aiNews}})

	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{Channel: "a2a", ConversationID: "a2a:rank-test", Text: `{"profile":"geek-news","input":{"date":"2026-06-28"}}`})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var got geekNewsReply
	if err := json.Unmarshal([]byte(reply.Text), &got); err != nil {
		t.Fatalf("Unmarshal(reply) error = %v", err)
	}
	if got.News[0].Link != news[1].Link || got.AINews[0].Link != aiNews[1].Link {
		t.Fatalf("news groups were not ranked independently: news=%q ai_news=%q", got.News[0].Link, got.AINews[0].Link)
	}
}

func TestProcessGeekNewsItemsRetriesOnlyInvalidBatchItems(t *testing.T) {
	items := fiveSourceNews()[:2]
	for i := range items {
		items[i].Description = "short"
	}
	batch := geekNewsBatchTranslations{Items: []geekNewsBatchTranslation{
		{ID: 0, Title: "中文标题 0", Description: strings.Repeat("详", 299)},
		{ID: 1, Title: "中文标题 1", Description: strings.Repeat("详", 300)},
	}}
	data, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	agent := &fakeA2AAgent{structuredArgumentQueue: []string{string(data), translatedNewsResponse(0, 300)}}
	pipeline := newA2APipeline(agent, nil)

	processed := pipeline.processGeekNewsItems(context.Background(), a2a.ConversationTurn{Channel: "a2a", ConversationID: "a2a:test"}, a2aPromptProfileSpec{}, "news", items)
	if agent.profileCalls != 2 || agent.profileRequests[0].Name != "geek-news-batch-translation" || agent.profileRequests[1].Name != "geek-news-description" {
		t.Fatalf("profile requests = %#v, want batch translation plus one description retry", agent.profileRequests)
	}
	if !geekNewsDescriptionValid(processed[0].Description) || !geekNewsDescriptionValid(processed[1].Description) {
		t.Fatalf("processed = %#v, want both descriptions accepted", processed)
	}
}

func TestA2APipelineRejectsIncompleteTranslationForEachNewsGroup(t *testing.T) {
	news := make([]geekNewsItem, 13)
	aiNews := make([]geekNewsItem, 14)
	for i := range news {
		news[i] = geekNewsItem{Link: "https://example.com/news/" + strconv.Itoa(i), Title: "source " + strconv.Itoa(i), Description: "short", CreateTime: 1719532800 + int64(i)}
	}
	for i := range aiNews {
		aiNews[i] = geekNewsItem{Link: "https://example.com/ai/" + strconv.Itoa(i), Title: "source " + strconv.Itoa(i), Description: "short", CreateTime: 1719532900 + int64(i)}
	}
	agent := &fakeA2AAgent{structuredArguments: `{invalid`}
	pipeline := newA2APipelineWithNewsFetcher(agent, nil, fakeGeekNewsFetcher{batch: geekNewsReply{CreateTime: 1719532800, News: news, AINews: aiNews}})

	if _, err := pipeline.Run(context.Background(), a2a.ConversationTurn{Channel: "a2a", Text: `{"profile":"geek-news","input":{"date":"2026-06-28"}}`}); err == nil {
		t.Fatal("Run() error = nil, want acceptance rejection for untranslated groups")
	}
}

func TestTranslateGeekNewsDescriptionRetriesShortDescription(t *testing.T) {
	agent := &fakeA2AAgent{structuredArgumentQueue: []string{
		translatedNewsResponse(0, 299),
		translatedNewsResponse(0, 300),
	}}
	pipeline := newA2APipeline(agent, nil)

	description, err := pipeline.translateGeekNewsDescription(context.Background(), a2a.ConversationTurn{Channel: "a2a", ConversationID: "a2a:test"}, a2aPromptProfileSpec{}, "news", 0, fiveSourceNews()[0])
	if err != nil {
		t.Fatalf("translateGeekNewsDescription() error = %v", err)
	}
	if !geekNewsDescriptionValid(description) || countChineseRunes(description) != 300 {
		t.Fatalf("description Chinese characters = %d, want 300", countChineseRunes(description))
	}
	if agent.profileCalls != 2 {
		t.Fatalf("profile calls = %d, want retry after short description", agent.profileCalls)
	}
}

func TestGeekNewsDescriptionStructuredOutputRejectsLongDescription(t *testing.T) {
	output := newGeekNewsDescriptionStructuredOutput()
	if _, err := output.Capture(translatedNewsResponse(0, 501)); err == nil {
		t.Fatal("Capture() error = nil, want overlong description rejection")
	}
}

func TestGeekNewsDescriptionStructuredOutputRejectsEnglishAtValidRuneLength(t *testing.T) {
	output := newGeekNewsDescriptionStructuredOutput()
	value := `{"description":"` + strings.Repeat("english content ", 30) + `"}`
	if _, err := output.Capture(value); err == nil {
		t.Fatal("Capture() error = nil, want English description rejection")
	}
}

func TestA2APipelineGeekNewsRejectsUnacceptedSourceFallback(t *testing.T) {
	items := fiveSourceNews()
	agent := &fakeA2AAgent{structuredArguments: `{invalid`}
	pipeline := newA2APipelineWithNewsFetcher(agent, nil, fakeGeekNewsFetcher{batch: geekNewsReply{CreateTime: 1719532800, News: items}})

	if _, err := pipeline.Run(context.Background(), a2a.ConversationTurn{Channel: "a2a", Text: `{"profile":"geek-news","input":{"date":"2026-06-28"}}`}); err == nil {
		t.Fatal("Run() error = nil, want final acceptance rejection")
	}
}

func TestA2APipelineNormalizesGeekNewsProfileJSON(t *testing.T) {
	agent := &fakeA2AAgent{profileReply: `{
		"create_time":1719532800,
		"summary":"今日科技新闻",
		"news":[{
			"link":"https://example.com/news",
			"title":"标题",
			"description":"实现\"模型自由\"的实践",
			"image":"https://example.com/cover.jpg",
			"create_time":1719532800
		}]
	}`}
	pipeline := newA2APipeline(agent, nil)

	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel: "a2a",
		Text:    `{"profile":"geek-news","input":{"date":"2026-06-28"}}`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !json.Valid([]byte(reply.Text)) {
		t.Fatalf("reply = %q, want valid JSON", reply.Text)
	}
	if strings.Contains(reply.Text, `实现"模型自由"`) {
		t.Fatalf("reply = %q, want embedded quote escaped by json.Marshal", reply.Text)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(reply.Text), &got); err != nil {
		t.Fatalf("Unmarshal(reply) error = %v", err)
	}
}

func TestA2APipelineRejectsInvalidGeekNewsProfileJSON(t *testing.T) {
	agent := &fakeA2AAgent{profileReply: `{"create_time":1719532800,"summary":"今日科技新闻","news":[{"link":"https://example.com/news","title":"标题","description":"实现"模型自由"","image":"","create_time":1719532800}]}`}
	pipeline := newA2APipeline(agent, nil)

	if _, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel: "a2a",
		Text:    `{"profile":"geek-news","input":{"date":"2026-06-28"}}`,
	}); err == nil {
		t.Fatal("Run() error = nil, want invalid geek-news JSON error")
	}
}

func TestA2APipelineRepairsInvalidGeekNewsProfileJSONOnce(t *testing.T) {
	agent := &fakeA2AAgent{profileReplies: []string{
		`{"create_time":1719532800,"summary":"今日科技新闻","news":[{"link":"https://example.com/news","title":"标题","description":"核心理念是"Agent 负责记忆，人类专注创新"。","image":"","create_time":1719532800}]}`,
		`{"create_time":1719532800,"summary":"今日科技新闻","news":[{"link":"https://example.com/news","title":"标题","description":"核心理念是\"Agent 负责记忆，人类专注创新\"。","image":"","create_time":1719532800}]}`,
	}}
	pipeline := newA2APipeline(agent, nil)

	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel:        "a2a",
		ConversationID: "a2a:partner_a:cyeam_web",
		Text:           `{"profile":"geek-news","input":{"date":"2026-06-28"}}`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !json.Valid([]byte(reply.Text)) {
		t.Fatalf("reply = %q, want valid JSON", reply.Text)
	}
	if agent.profileCalls != 2 {
		t.Fatalf("profileCalls = %d, want original + repair", agent.profileCalls)
	}
	if len(agent.profileRequests) != 2 {
		t.Fatalf("profileRequests = %d, want 2", len(agent.profileRequests))
	}
	repair := agent.profileRequests[1]
	if repair.Name != "geek-news-json-repair" {
		t.Fatalf("repair.Name = %q, want geek-news-json-repair", repair.Name)
	}
	if repair.AllowTools {
		t.Fatal("repair.AllowTools = true, want false")
	}
	if !repair.DisableHistory {
		t.Fatal("repair.DisableHistory = false, want true")
	}
	if !strings.Contains(repair.UserText, "invalid_json") || !strings.Contains(repair.UserText, "err_offset") {
		t.Fatalf("repair.UserText = %q, want invalid_json and err_offset", repair.UserText)
	}
}

func TestA2APipelineRepairsInvalidStructuredOutputArguments(t *testing.T) {
	rawArguments := `{"create_time":1719532800,"summary":"今日科技新闻","news":[{"link":"https://example.com/news","title":"标题","description":"采用"UI"设计","image":"","create_time":1719532800}]}`
	repaired := `{"create_time":1719532800,"summary":"今日科技新闻","news":[{"link":"https://example.com/news","title":"标题","description":"采用中文界面设计","image":"","create_time":1719532800}]}`
	agent := &fakeA2AAgent{
		structuredArguments: rawArguments,
		profileReplies:      []string{repaired},
	}
	pipeline := newA2APipeline(agent, nil)

	var logs bytes.Buffer
	oldLogger := logger.StdLogger
	logger.StdLogger = logger.NewWriterLogger(&logs, 0, 2)
	t.Cleanup(func() {
		logger.StdLogger = oldLogger
	})

	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel:        "a2a",
		ConversationID: "a2a:partner_a:cyeam_web",
		Text:           `{"profile":"geek-news","input":{"date":"2026-06-28"}}`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !json.Valid([]byte(reply.Text)) {
		t.Fatalf("reply = %q, want repaired valid JSON", reply.Text)
	}
	if agent.profileCalls != 2 {
		t.Fatalf("profileCalls = %d, want structured output attempt + repair", agent.profileCalls)
	}
	gotLogs := logs.String()
	for _, want := range []string{
		"[A2A][geek-news][structured_output_invalid]",
		"invalid character 'U'",
		"err_offset=",
		"args_near=",
		"args_preview=",
		"[A2A][geek-news][structured_output_repaired]",
	} {
		if !strings.Contains(gotLogs, want) {
			t.Fatalf("logs = %q, want substring %q", gotLogs, want)
		}
	}
}

func TestA2APipelineLogsRawGeekNewsReplyOnNormalizeFailure(t *testing.T) {
	rawReply := `{"create_time":1719532800,"summary":"今日科技新闻","news":[{"link":"https://example.com/news","title":"标题","description":"实现"模型自由"","image":"","create_time":1719532800}]}`
	agent := &fakeA2AAgent{profileReply: rawReply}
	pipeline := newA2APipeline(agent, nil)

	var logs bytes.Buffer
	oldLogger := logger.StdLogger
	logger.StdLogger = logger.NewWriterLogger(&logs, 0, 2)
	t.Cleanup(func() {
		logger.StdLogger = oldLogger
	})

	if _, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel:        "a2a",
		ConversationID: "a2a:partner_a:cyeam_web",
		Text:           `{"profile":"geek-news","input":{"date":"2026-06-28"}}`,
	}); err == nil {
		t.Fatal("Run() error = nil, want invalid geek-news JSON error")
	}

	got := logs.String()
	for _, want := range []string{
		"[A2A][geek-news][normalize_failed]",
		"reply_len=" + strconv.Itoa(len(rawReply)),
		"err_offset=",
		"reply_near=",
		`实现\"模型自由\"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs = %q, want substring %q", got, want)
		}
	}
}

func TestA2APipelineRoutesPlainTextToPublicAssistant(t *testing.T) {
	agent := &fakeA2AAgent{}
	pipeline := newA2APipeline(agent, nil)

	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel:        "a2a",
		ConversationID: "a2a:partner_a:cyeam_web",
		Text:           "北京天气",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if reply.Text != "subagent reply" {
		t.Fatalf("reply = %q, want subagent reply", reply.Text)
	}
	if agent.profileCalls != 0 || agent.subAgentCalls != 1 {
		t.Fatalf("calls profile=%d subagent=%d, want subagent only", agent.profileCalls, agent.subAgentCalls)
	}
	if agent.lastSubAgent != "a2a_public_assistant" {
		t.Fatalf("lastSubAgent = %q, want a2a_public_assistant", agent.lastSubAgent)
	}
	if agent.lastSubSessionKey != "a2a:partner_a:cyeam_web" {
		t.Fatalf("lastSubSessionKey = %q, want session key kept for A2A tracing", agent.lastSubSessionKey)
	}
}

func TestA2APipelineStreamsOnlyArchitectProfile(t *testing.T) {
	agent := &fakeA2AAgent{}
	pipeline := newA2APipeline(agent, nil)
	var got []a2a.ConversationStreamEvent

	reply, err := pipeline.RunStream(context.Background(), a2a.ConversationTurn{
		Channel:        "a2a",
		ConversationID: "a2a:partner_a:cyeam_web",
		Text:           `{"profile":"architect","input":"怎么设计 A2A 流式？"}`,
	}, func(ev a2a.ConversationStreamEvent) bool {
		got = append(got, ev)
		return true
	})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if reply.Text != "架构建议" || reply.Reasoning != "先看边界。" {
		t.Fatalf("reply = %#v, want answer and reasoning", reply)
	}
	if len(got) != 0 {
		t.Fatalf("stream events = %#v, want no synthetic reasoning/progress delta", got)
	}
	if agent.profileStreamCalls != 1 || agent.profileCalls != 0 || agent.subAgentCalls != 0 {
		t.Fatalf("calls stream=%d profile=%d subagent=%d, want stream only", agent.profileStreamCalls, agent.profileCalls, agent.subAgentCalls)
	}
	if !agent.lastProfile.DisableHistory {
		t.Fatalf("DisableHistory = false, want A2A stream profile without memory")
	}
}

func TestA2APipelineRunStreamRejectsNonArchitectProfile(t *testing.T) {
	agent := &fakeA2AAgent{}
	pipeline := newA2APipeline(agent, nil)

	if _, err := pipeline.RunStream(context.Background(), a2a.ConversationTurn{
		Channel: "a2a",
		Text:    `{"profile":"geek-news","input":{"date":"2026-06-28"}}`,
	}, func(a2a.ConversationStreamEvent) bool { return true }); err == nil {
		t.Fatal("RunStream() error = nil, want unsupported profile")
	}
	if agent.profileStreamCalls != 0 || agent.profileCalls != 0 || agent.subAgentCalls != 0 {
		t.Fatalf("calls stream=%d profile=%d subagent=%d, want none", agent.profileStreamCalls, agent.profileCalls, agent.subAgentCalls)
	}
}

func TestA2APipelineHandlesTypedNilAgent(t *testing.T) {
	var agent *fakeA2AAgent
	pipeline := newA2APipeline(agent, nil)

	if _, err := pipeline.Run(context.Background(), a2a.ConversationTurn{Text: "hi"}); err == nil {
		t.Fatal("Run() error = nil, want agent not available")
	}
}

func TestParseA2AProfileRequestExtractsProfileAndJSONInput(t *testing.T) {
	req, ok := parseA2AProfileRequest(`{"profile":"encouragement","input":{"date":"6月27日周六","type":"休息日"}}`)
	if !ok {
		t.Fatal("parseA2AProfileRequest() ok = false, want true")
	}
	if req.Profile != "encouragement" {
		t.Fatalf("Profile = %q, want encouragement", req.Profile)
	}
	if req.UserText != `{"date":"6月27日周六","type":"休息日"}` {
		t.Fatalf("UserText = %q, want compact input JSON", req.UserText)
	}
}

func TestParseA2AProfileRequestExtractsStringInput(t *testing.T) {
	req, ok := parseA2AProfileRequest(`{"profile":"architect","input":"怎么设计 A2A profile 路由？"}`)
	if !ok {
		t.Fatal("parseA2AProfileRequest() ok = false, want true")
	}
	if req.Profile != "architect" {
		t.Fatalf("Profile = %q, want architect", req.Profile)
	}
	if req.UserText != "怎么设计 A2A profile 路由？" {
		t.Fatalf("UserText = %q, want raw string input", req.UserText)
	}
}

func TestParseA2AProfileRequestRejectsPlainText(t *testing.T) {
	if _, ok := parseA2AProfileRequest("北京天气"); ok {
		t.Fatal("parseA2AProfileRequest() ok = true, want false")
	}
}

func TestA2APromptProfilesDefineEncouragementAndArchitect(t *testing.T) {
	encouragement, ok := a2aPromptProfile("encouragement")
	if !ok {
		t.Fatal("encouragement profile missing")
	}
	if !encouragement.AllowTools {
		t.Fatal("encouragement profile should allow holiday skill")
	}
	if encouragement.MaxSteps != 0 {
		t.Fatalf("encouragement MaxSteps = %d, want 0 to use runtime default", encouragement.MaxSteps)
	}
	if !strings.Contains(encouragement.SystemPrompt, "holiday") || !strings.Contains(encouragement.SystemPrompt, "只接收 date") {
		t.Fatalf("encouragement prompt = %q, want date-only input and holiday skill guidance", encouragement.SystemPrompt)
	}
	if !strings.Contains(encouragement.SystemPrompt, "maps_weather") || !strings.Contains(encouragement.SystemPrompt, "北京") {
		t.Fatalf("encouragement prompt = %q, want maps_weather guidance for Beijing", encouragement.SystemPrompt)
	}
	if !strings.Contains(encouragement.SystemPrompt, "cyeam tv today") {
		t.Fatalf("encouragement prompt = %q, want cyeam tv today guidance", encouragement.SystemPrompt)
	}

	architect, ok := a2aPromptProfile("architect")
	if !ok {
		t.Fatal("architect profile missing")
	}
	if !architect.AllowTools {
		t.Fatal("architect profile should allow MCP tools")
	}
	if architect.MaxSteps != 0 {
		t.Fatalf("architect MaxSteps = %d, want 0 to use runtime default", architect.MaxSteps)
	}
	if architect.SystemPrompt == "" {
		t.Fatal("architect profile should define system prompt")
	}

	geekNews, ok := a2aPromptProfile("geek-news")
	if !ok {
		t.Fatal("geek-news profile missing")
	}
	if !geekNews.AllowTools {
		t.Fatal("geek-news profile should allow news skill")
	}
	if !strings.Contains(geekNews.SystemPrompt, "news skill") ||
		!strings.Contains(geekNews.SystemPrompt, "data 字段是 JSON 字符串") ||
		!strings.Contains(geekNews.SystemPrompt, "ai_news") ||
		!strings.Contains(geekNews.SystemPrompt, "两个 Tab") ||
		!strings.Contains(geekNews.SystemPrompt, "image") ||
		!strings.Contains(geekNews.SystemPrompt, "create_time") ||
		!strings.Contains(geekNews.SystemPrompt, "交付目标") ||
		!strings.Contains(geekNews.SystemPrompt, "验收通过后") {
		t.Fatalf("geek-news prompt = %q, want news skill guidance and GeekNews JSON fields", geekNews.SystemPrompt)
	}
}

func TestEncouragementDateGuardOverrideTodayReturnsEmpty(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, loc)
	today := now.Format("2006-01-02")
	cases := []string{
		`{"date":"` + today + `"}`,
	}
	for _, ut := range cases {
		if got := encouragementDateGuardOverride(ut, now); got != "" {
			t.Fatalf("encouragementDateGuardOverride(%q) = %q, want empty for today/unknown", ut, got)
		}
	}
}

func TestEncouragementDateGuardOverrideUnknownDateForbidsTools(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, loc)
	cases := []string{
		`{"other":"value"}`,
		`{"date":"6月27日周六","type":"休息日"}`,
		`not-json`,
		``,
	}
	for _, ut := range cases {
		if got := encouragementDateGuardOverride(ut, now); got == "" {
			t.Fatalf("encouragementDateGuardOverride(%q) empty, want tool guard for unknown date", ut)
		}
	}
}

func TestEncouragementDateGuardOverrideNonTodayForbidsTools(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, loc)
	cases := []string{
		`{"date":"2026-07-15"}`, // 昨天
		`{"date":"2026-07-17"}`, // 明天
		`{"date":"2020-01-01"}`, // 历史
	}
	for _, ut := range cases {
		got := encouragementDateGuardOverride(ut, now)
		if got == "" {
			t.Fatalf("encouragementDateGuardOverride(%q) empty, want override text for non-today date", ut)
		}
		if !strings.Contains(got, "maps_weather") || !strings.Contains(got, "cyeam tv today") {
			t.Fatalf("encouragementDateGuardOverride(%q) = %q, want mention of maps_weather and cyeam tv today", ut, got)
		}
	}
}

func TestA2APipelineAppendsEncouragementDateGuardForNonTodayDate(t *testing.T) {
	agent := &fakeA2AAgent{}
	pipeline := newA2APipeline(agent, nil)

	// 用 2020-01-01 这种确定不是服务运行当天的日期，触发 guard。
	_, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel: "a2a",
		Text:    `{"profile":"encouragement","input":{"date":"2020-01-01"}}`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if agent.lastProfile.Name != "encouragement" {
		t.Fatalf("lastProfile.Name = %q, want encouragement", agent.lastProfile.Name)
	}
	if !strings.Contains(agent.lastProfile.SystemPrompt, "重要覆盖规则") ||
		!strings.Contains(agent.lastProfile.SystemPrompt, "严禁调用 maps_weather") ||
		!strings.Contains(agent.lastProfile.SystemPrompt, "严禁调用 cyeam tv today") {
		t.Fatalf("SystemPrompt missing date-guard override:\n%s", agent.lastProfile.SystemPrompt)
	}
}

func TestGeekNewsPartialDeliveryKeepsAcceptedItems(t *testing.T) {
	items := fiveSourceNews()[:2]
	batch, _ := json.Marshal(geekNewsBatchTranslations{Items: []geekNewsBatchTranslation{
		{ID: 0, Title: "合格新闻", Description: strings.Repeat("详", 300)},
		{ID: 1, Title: "失败新闻", Description: "太短"},
	}})
	agent := &fakeA2AAgent{structuredArgumentQueue: []string{string(batch), translatedNewsResponse(0, 299), translatedNewsResponse(0, 299), `{"ids":["n0"]}`}}
	pipeline := newA2APipelineWithNewsFetcher(agent, nil, fakeGeekNewsFetcher{batch: geekNewsReply{News: items}})
	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{Channel: "a2a", Text: `{"profile":"geek-news","input":{"date":"2026-06-28"}}`})
	if err != nil {
		t.Fatal(err)
	}
	var got geekNewsReply
	if err := json.Unmarshal([]byte(reply.Text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.News) != 1 || got.News[0].Link != items[0].Link {
		t.Fatalf("unexpected delivery: %#v", got.News)
	}
	for _, req := range agent.profileRequests {
		if req.Name == "geek-news-description" && !strings.Contains(req.SessionKey, ":item:1:") {
			t.Fatalf("retried accepted item: %s", req.SessionKey)
		}
	}
}

func TestGeekNewsRepairsCurrentBatchBeforeNextBatch(t *testing.T) {
	items := fiveSourceNews()[:4]
	batch, _ := json.Marshal(geekNewsBatchTranslations{Items: []geekNewsBatchTranslation{
		{ID: 0, Title: "待修正", Description: "太短"},
		{ID: 1, Title: "合格一", Description: strings.Repeat("详", 300)},
		{ID: 2, Title: "合格二", Description: strings.Repeat("详", 300)},
	}})
	agent := &fakeA2AAgent{structuredArgumentQueue: []string{string(batch), translatedNewsResponse(0, 300), translatedNewsBatchResponse(1, 300)}}
	got := newA2APipeline(agent, nil).processGeekNewsItems(context.Background(), a2a.ConversationTurn{}, a2aPromptProfileSpec{}, "news", items)
	if len(got) != 4 || agent.profileCalls != 3 || agent.profileRequests[1].Name != "geek-news-description" || agent.profileRequests[2].Name != "geek-news-batch-translation" {
		t.Fatalf("items=%d requests=%#v", len(got), agent.profileRequests)
	}
}

func TestGeekNewsCancelledProcessingDoesNotCallModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	agent := &fakeA2AAgent{}
	got := newA2APipeline(agent, nil).processGeekNewsItems(ctx, a2a.ConversationTurn{}, a2aPromptProfileSpec{}, "news", fiveSourceNews())
	if len(got) != 0 || agent.profileCalls != 0 {
		t.Fatalf("items=%d calls=%d", len(got), agent.profileCalls)
	}
}

func TestGeekNewsDescriptionRetryIncludesFailedCandidate(t *testing.T) {
	agent := &fakeA2AAgent{structuredArgumentQueue: []string{translatedNewsResponse(0, 299), translatedNewsResponse(0, 300)}}
	_, err := newA2APipeline(agent, nil).translateGeekNewsDescription(context.Background(), a2a.ConversationTurn{}, a2aPromptProfileSpec{}, "news", 0, fiveSourceNews()[0])
	if err != nil {
		t.Fatal(err)
	}
	var feedback struct {
		Previous string `json:"previous_description"`
		Error    string `json:"validation_error"`
	}
	if err := json.Unmarshal([]byte(agent.profileRequests[1].UserText), &feedback); err != nil {
		t.Fatal(err)
	}
	if countChineseRunes(feedback.Previous) != 299 || !strings.Contains(feedback.Error, "299") {
		t.Fatalf("feedback=%#v", feedback)
	}
}

type geekNewsTailErrorAgent struct{ fakeA2AAgent }

func (f *geekNewsTailErrorAgent) RunPromptProfile(ctx context.Context, req agentruntime.PromptProfileRequest) (string, error) {
	_, err := f.fakeA2AAgent.RunPromptProfile(ctx, req)
	if err != nil {
		return "", err
	}
	return "", context.DeadlineExceeded
}

func TestGeekNewsKeepsCapturedBatchAfterRunnerError(t *testing.T) {
	agent := &geekNewsTailErrorAgent{fakeA2AAgent{structuredArguments: translatedNewsBatchResponse(2, 300)}}
	got := newA2APipeline(agent, nil).processGeekNewsItems(context.Background(), a2a.ConversationTurn{}, a2aPromptProfileSpec{}, "news", fiveSourceNews()[:2])
	if len(got) != 2 || agent.profileCalls != 1 {
		t.Fatalf("items=%d calls=%d, want captured results without retry", len(got), agent.profileCalls)
	}
}
