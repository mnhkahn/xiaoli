package admin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestVoiceConversationKeepsOnlyTwoTurnsPerDevice(t *testing.T) {
	v := &voiceConversation{history: make(map[string][]*schema.Message)}
	for _, question := range []string{"第一问", "第二问", "第三问"} {
		v.remember("c3", question, question+"的回答")
	}
	messages := v.messages("c3", "第四问")
	if len(messages) != 6 {
		t.Fatalf("messages = %d, want system + two turns + current", len(messages))
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "第一问") {
			t.Fatalf("old voice turn leaked into prompt: %q", message.Content)
		}
	}
	if len(v.messages("s3", "独立设备")) != 2 {
		t.Fatal("voice history leaked between devices")
	}
}

func TestVoiceSentenceEmitsEarlyNaturalChunks(t *testing.T) {
	cases := []struct {
		input      string
		final      bool
		want, rest string
	}{
		{"你好。后一句", false, "你好。", "后一句"},
		{"这是一个确实比较长的前半句话，后面继续", false, "这是一个确实比较长的前半句话，", "后面继续"},
		{"短句", false, "", "短句"},
		{"短句", true, "短句", ""},
	}
	for _, tc := range cases {
		got, rest := voiceSentence(tc.input, tc.final)
		if got != tc.want || rest != tc.rest {
			t.Errorf("voiceSentence(%q) = (%q,%q), want (%q,%q)", tc.input, got, rest, tc.want, tc.rest)
		}
	}
}

func TestRejectVoiceAnswer(t *testing.T) {
	cases := []struct {
		input, answer, want string
	}{
		{"你介绍一下你自己", "你介绍一下你自己。", "input_echo"},
		{"北京天气怎么样", "嗯，用户问的是北京天气。我需要先检查一下有没有工具。", "self_analysis"},
		{"你会唱歌吗", "嗯，用户问我能不能唱首歌。不要输出思考过程、Markdown、链接或工具细节。", "prompt_echo"},
		{"你会唱歌吗", "不能回复Markdown工具调用，两句话之内回复。", "prompt_echo"},
		{"你是谁", "我是小李，可以陪你聊天。", ""},
		{"什么是 Markdown", "Markdown 是一种方便排版的文本格式。", ""},
	}
	for _, tc := range cases {
		if got := rejectVoiceAnswer(tc.input, tc.answer); got != tc.want {
			t.Errorf("rejectVoiceAnswer(%q, %q) = %q, want %q", tc.input, tc.answer, got, tc.want)
		}
	}
}

func TestEmitVoiceAnswerPreservesSentences(t *testing.T) {
	var parts []string
	if err := emitVoiceAnswer("你好。我是小李。", func(part string) error {
		parts = append(parts, part)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(parts, ""); got != "你好。我是小李。" {
		t.Fatalf("emitted %q", got)
	}
}

type voiceRetryModel struct {
	answers []string
	inputs  [][]*schema.Message
}

func (m *voiceRetryModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	panic("Generate is not used by voiceConversation")
}

func (m *voiceRetryModel) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.inputs = append(m.inputs, input)
	answer := m.answers[len(m.inputs)-1]
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(answer, nil)}), nil
}

func (m *voiceRetryModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func TestVoiceConversationRetriesWithoutSpeakingOrRememberingBadAnswer(t *testing.T) {
	fake := &voiceRetryModel{answers: []string{
		"嗯，用户问我是谁。不要输出思考过程、Markdown、链接或工具细节。",
		"我是小李，可以陪你聊天。",
	}}
	v := &voiceConversation{model: fake, history: make(map[string][]*schema.Message)}
	v.remember("c3", "上次的问题", "上次的坏回答")
	var spoken []string
	err := v.AnswerDeviceTextStream(context.Background(), "c3", "你是谁", func(part string) error {
		spoken = append(spoken, part)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(spoken, ""); got != "我是小李，可以陪你聊天。" {
		t.Fatalf("spoken = %q", got)
	}
	if len(fake.inputs) != 2 || len(fake.inputs[1]) != 2 {
		t.Fatalf("retry inputs = %#v", fake.inputs)
	}
	for _, message := range v.history["c3"] {
		if strings.Contains(message.Content, "坏回答") || strings.Contains(message.Content, "不要输出") {
			t.Fatalf("bad answer remained in memory: %q", message.Content)
		}
	}
}

func TestVoiceConversationStopsAfterOneBadRetry(t *testing.T) {
	fake := &voiceRetryModel{answers: []string{
		"你是谁。",
		"嗯，用户问我是谁。",
	}}
	v := &voiceConversation{model: fake, history: make(map[string][]*schema.Message)}
	var spoken []string
	err := v.AnswerDeviceTextStream(context.Background(), "c3", "你是谁", func(part string) error {
		spoken = append(spoken, part)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.inputs) != 2 || len(spoken) != 1 || spoken[0] != "刚才回答出了问题，请再问我一次。" {
		t.Fatalf("requests=%d spoken=%q", len(fake.inputs), spoken)
	}
	if len(v.history["c3"]) != 0 {
		t.Fatal("bad retry was remembered")
	}
}

func TestVoiceAdapterDoesNotUseFullAgentWhenVoiceConfigured(t *testing.T) {
	called := false
	a := &deviceConversationAdapter{
		voice: &voiceConversation{history: make(map[string][]*schema.Message)},
		pipeline: &ConversationPipeline{chat: conversationChatFunc(func(context.Context, ConversationTurn) (string, error) {
			called = true
			return "full agent", nil
		})},
	}
	_, _ = a.AnswerDeviceText(context.Background(), "c3", "你好")
	if called {
		t.Fatal("device voice entered the full Agent")
	}
}

func TestVoiceToolWhitelist(t *testing.T) {
	v := &voiceConversation{devices: voiceDeviceStub{tools: ToolListResponse{Ready: true, Tools: []map[string]any{
		{"name": "self.get_device_status", "description": "status"},
		{"name": "self.audio_speaker.set_volume", "description": "volume"},
		{"name": "self.camera.take_photo", "description": "photo"},
		{"name": "external.web_search", "description": "search"},
	}}}}
	tools := v.availableTools(context.Background(), "c3")
	if len(tools) != 3 {
		t.Fatalf("offered %d tools, want only three device tools", len(tools))
	}
	if !voiceToolOffered(tools, "set_volume") || voiceToolOffered(tools, "external.web_search") {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestVoiceWebToolsAvailableWithoutDevice(t *testing.T) {
	v := newVoiceConversation(Config{})
	tools := v.availableTools(context.Background(), "c3")
	if len(tools) != 2 || !voiceToolOffered(tools, "websearch") || !voiceToolOffered(tools, "webfetch") {
		t.Fatalf("voice web tools = %+v", tools)
	}
}

type voiceWebToolStub struct {
	name string
	args []map[string]any
}

func (s *voiceWebToolStub) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name, Desc: s.name}, nil
}

func (s *voiceWebToolStub) InvokableRun(_ context.Context, raw string, _ ...tool.Option) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", err
	}
	s.args = append(s.args, args)
	return `{"content":"资料"}`, nil
}

type voiceWebModelStub struct {
	responses []*schema.Message
	inputs    [][]*schema.Message
	toolSets  [][]*schema.ToolInfo
}

func (m *voiceWebModelStub) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	panic("Generate is not used by voiceConversation")
}

func (m *voiceWebModelStub) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.inputs = append(m.inputs, append([]*schema.Message(nil), input...))
	return schema.StreamReaderFromArray([]*schema.Message{m.responses[len(m.inputs)-1]}), nil
}

func (m *voiceWebModelStub) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.toolSets = append(m.toolSets, tools)
	return m, nil
}

func TestVoiceSearchThenFetchUsesOnlyTwoWebActions(t *testing.T) {
	search := &voiceWebToolStub{name: "websearch"}
	fetch := &voiceWebToolStub{name: "webfetch"}
	m := &voiceWebModelStub{responses: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{ID: "search-1", Function: schema.FunctionCall{Name: "websearch", Arguments: `{"query":"北京天气"}`}}}),
		schema.AssistantMessage("", []schema.ToolCall{{ID: "fetch-1", Function: schema.FunctionCall{Name: "webfetch", Arguments: `{"url":"https://example.com/weather"}`}}}),
		schema.AssistantMessage("北京今天适合出门。", nil),
	}}
	v := &voiceConversation{
		model: m, history: make(map[string][]*schema.Message),
		webTools: map[string]tool.InvokableTool{"websearch": search, "webfetch": fetch},
	}
	var spoken []string
	if err := v.AnswerDeviceTextStream(context.Background(), "c3", "北京今天适合出门吗", func(s string) error {
		spoken = append(spoken, s)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(m.inputs) != 3 || len(m.toolSets) != 2 || len(m.toolSets[1]) != 1 || m.toolSets[1][0].Name != "webfetch" {
		t.Fatalf("model requests=%d tool sets=%+v", len(m.inputs), m.toolSets)
	}
	if len(search.args) != 1 || search.args[0]["count"] != float64(3) || len(fetch.args) != 1 || fetch.args[0]["format"] != "text" || fetch.args[0]["timeout"] != float64(8) {
		t.Fatalf("search args=%+v fetch args=%+v", search.args, fetch.args)
	}
	if strings.Join(spoken, "") != "北京今天适合出门。" {
		t.Fatalf("spoken=%q", spoken)
	}
}

type voiceDeviceStub struct{ tools ToolListResponse }

func (d voiceDeviceStub) Tools(context.Context, string) (ToolListResponse, error) {
	return d.tools, nil
}
func (d voiceDeviceStub) Devices(context.Context) ([]Device, error) { return nil, nil }
func (d voiceDeviceStub) Call(context.Context, BridgeCallRequest) (BridgeCallResult, error) {
	return BridgeCallResult{}, nil
}
func (d voiceDeviceStub) Speak(context.Context, string, string) (map[string]any, error) {
	return nil, nil
}
func (d voiceDeviceStub) StopSpeak(context.Context, string) (map[string]any, error) { return nil, nil }
