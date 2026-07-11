package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mnhkahn/gogogo/logger"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	a2a "github.com/mnhkahn/xiaoli/server/internal/a2a"
)

type fakeA2AAgent struct {
	profileCalls        int
	profileStreamCalls  int
	subAgentCalls       int
	lastProfile         agentruntime.PromptProfileRequest
	profileRequests     []agentruntime.PromptProfileRequest
	lastSubAgent        string
	lastSubSessionKey   string
	profileReply        string
	profileReplies      []string
	structuredArguments string
}

func (f *fakeA2AAgent) RunPromptProfile(ctx context.Context, req agentruntime.PromptProfileRequest) (string, error) {
	f.profileCalls++
	f.lastProfile = req
	f.profileRequests = append(f.profileRequests, req)
	if req.StructuredOutput != nil && f.structuredArguments != "" {
		_, err := req.StructuredOutput.Capture(f.structuredArguments)
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
		!strings.Contains(geekNews.SystemPrompt, "只返回内层的 news 对象") ||
		!strings.Contains(geekNews.SystemPrompt, "image") ||
		!strings.Contains(geekNews.SystemPrompt, "create_time") {
		t.Fatalf("geek-news prompt = %q, want news skill guidance and GeekNews JSON fields", geekNews.SystemPrompt)
	}
}
