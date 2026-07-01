package admin

import (
	"context"
	"strings"
	"testing"

	a2a "github.com/mnhkahn/xiaoli-esp32/server/internal/a2a"
	agentruntime "github.com/mnhkahn/xiaoli-esp32/server/internal/agent/runtime"
)

type fakeA2AAgent struct {
	profileCalls       int
	profileStreamCalls int
	subAgentCalls      int
	lastProfile        agentruntime.PromptProfileRequest
	lastSubAgent       string
}

func (f *fakeA2AAgent) RunPromptProfile(ctx context.Context, req agentruntime.PromptProfileRequest) (string, error) {
	f.profileCalls++
	f.lastProfile = req
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
	agent := &fakeA2AAgent{}
	pipeline := newA2APipeline(agent, nil)

	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel:        "a2a",
		ConversationID: "a2a:partner_a:cyeam_web",
		Text:           `{"profile":"geek-news","input":{"date":"2026-06-28"}}`,
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
	if agent.lastProfile.Name != "geek-news" || agent.lastProfile.ChannelName != "a2a" || !agent.lastProfile.AllowTools {
		t.Fatalf("lastProfile = %#v, want geek-news profile with tools", agent.lastProfile)
	}
	if agent.lastProfile.SessionKey != "a2a:partner_a:cyeam_web" {
		t.Fatalf("SessionKey = %q, want a2a session key", agent.lastProfile.SessionKey)
	}
	if agent.lastProfile.UserText != `{"date":"2026-06-28"}` {
		t.Fatalf("UserText = %q, want compact date JSON", agent.lastProfile.UserText)
	}
}

func TestA2APipelineRoutesPlainTextToPublicAssistant(t *testing.T) {
	agent := &fakeA2AAgent{}
	pipeline := newA2APipeline(agent, nil)

	reply, err := pipeline.Run(context.Background(), a2a.ConversationTurn{
		Channel: "a2a",
		Text:    "北京天气",
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
