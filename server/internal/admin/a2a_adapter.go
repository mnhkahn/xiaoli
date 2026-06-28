package admin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	a2a "xiaoli/server/internal/a2a"
	agentruntime "xiaoli/server/internal/agent/runtime"
)

// a2aPipeline adapts the EinoAgent's named subagent invocation to the
// a2a.ConversationPipeline interface. It routes A2A requests to the
// a2a_public_assistant subagent, never to the main agent. The internal
// session ID (a2a:<key_id>:<context_id>) is used as the sessionKey so the
// subagent's memory is scoped to the calling partner and context, never
// to a personal device or Lark/WeChat session.
type a2aPipeline struct {
	agent a2aAgentRunner
}

var _ a2a.ConversationPipeline = (*a2aPipeline)(nil)

type a2aAgentRunner interface {
	RunNamedSubAgent(ctx context.Context, name string, prompt string, sessionKey string, channelName string) (string, error)
	RunPromptProfile(ctx context.Context, req agentruntime.PromptProfileRequest) (string, error)
}

func newA2APipeline(agent a2aAgentRunner) *a2aPipeline {
	return &a2aPipeline{agent: agent}
}

func (p *a2aPipeline) Run(ctx context.Context, turn a2a.ConversationTurn) (a2a.ConversationReply, error) {
	if a2aAgentRunnerNil(p.agent) {
		return a2a.ConversationReply{}, errors.New("agent not available")
	}
	text := strings.TrimSpace(turn.Text)
	if text == "" {
		return a2a.ConversationReply{}, errors.New("empty text")
	}
	if req, ok := parseA2AProfileRequest(text); ok {
		profile, ok := a2aPromptProfile(req.Profile)
		if !ok {
			return a2a.ConversationReply{}, errors.New("unknown profile")
		}
		reply, err := p.agent.RunPromptProfile(ctx, agentruntime.PromptProfileRequest{
			Name:         profile.Name,
			SystemPrompt: profile.SystemPrompt,
			UserText:     req.UserText,
			ChannelName:  turn.Channel,
			SessionKey:   turn.ConversationID,
			AllowTools:   profile.AllowTools,
			MaxSteps:     profile.MaxSteps,
		})
		if err != nil {
			return a2a.ConversationReply{}, err
		}
		return a2a.ConversationReply{Text: reply}, nil
	}
	reply, err := p.agent.RunNamedSubAgent(ctx, "a2a_public_assistant", text, turn.ConversationID, turn.Channel)
	if err != nil {
		return a2a.ConversationReply{}, err
	}
	return a2a.ConversationReply{Text: reply}, nil
}

func a2aAgentRunnerNil(agent a2aAgentRunner) bool {
	if agent == nil {
		return true
	}
	v := reflect.ValueOf(agent)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

type a2aProfileRequest struct {
	Profile  string
	UserText string
}

type a2aPromptProfileSpec struct {
	Name         string
	SystemPrompt string
	AllowTools   bool
	MaxSteps     int
}

type rawA2AProfileRequest struct {
	Profile string          `json:"profile"`
	Input   json.RawMessage `json:"input"`
}

func parseA2AProfileRequest(text string) (a2aProfileRequest, bool) {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "{") {
		return a2aProfileRequest{}, false
	}
	var raw rawA2AProfileRequest
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return a2aProfileRequest{}, false
	}
	profile := strings.TrimSpace(raw.Profile)
	if profile == "" || len(raw.Input) == 0 {
		return a2aProfileRequest{}, false
	}
	var inputString string
	if err := json.Unmarshal(raw.Input, &inputString); err == nil {
		inputString = strings.TrimSpace(inputString)
		if inputString == "" {
			return a2aProfileRequest{}, false
		}
		return a2aProfileRequest{Profile: profile, UserText: inputString}, true
	}
	compact := string(raw.Input)
	if !json.Valid(raw.Input) {
		return a2aProfileRequest{}, false
	}
	return a2aProfileRequest{Profile: profile, UserText: compact}, true
}

func a2aPromptProfile(name string) (a2aPromptProfileSpec, bool) {
	switch strings.TrimSpace(name) {
	case "encouragement":
		return a2aPromptProfileSpec{
			Name: "encouragement",
			SystemPrompt: "你是一个中文鼓励师。客户端只接收 date 字段，例如 {\"date\":\"2026-06-27\"}。\n" +
				"要求：\n" +
				"- 先使用 holiday skill 查询 date 对应的是工作日、休息日还是调休补班\n" +
				"- 只输出一句话\n" +
				"- 不要解释，不要分段\n" +
				"- 不要提及具体日期或星期\n" +
				"- 自然、真诚、有力\n" +
				"- 结合当天类型调整鼓励方向：工作日/调休补班鼓励踏实工作精进技术，休息日鼓励放松享受积蓄能量",
			AllowTools: true,
		}, true
	case "architect":
		return a2aPromptProfileSpec{
			Name: "architect",
			SystemPrompt: "你是一个中文软件架构师。参考 cyeam_web 的架构咨询风格，给出清晰、务实、可执行的系统设计建议。\n" +
				"要求：\n" +
				"- 优先通过可用的 MCP 工具查询 wiki 知识库中的公开资料\n" +
				"- 不使用 RAG，不声称已检索向量库\n" +
				"- 回答要聚焦架构边界、数据流、接口、风险和测试\n" +
				"- 信息不足时先指出假设，再给出保守建议\n" +
				"- 不访问个人数据、设备数据或私有会话",
			AllowTools: true,
		}, true
	case "geek-news":
		return a2aPromptProfileSpec{
			Name: "geek-news",
			SystemPrompt: "你是 cyeam_web 的科技新闻生成器。客户端只接收 date 字段，例如 {\"date\":\"2026-06-28\"}。\n" +
				"要求：\n" +
				"- 使用 news skill 查询该 date 的完整科技新闻，命令格式优先使用 cyeam news get --date <YYYY-MM-DD>\n" +
				"- 只输出一个可被 json.Unmarshal 直接解析的 JSON 对象，不要 Markdown，不要代码块，不要解释\n" +
				"- JSON 顶层字段必须是 create_time、news、summary\n" +
				"- news 是数组，每条包含 link、title、description、image、create_time\n" +
				"- summary 用中文概括最重要的 3-5 个趋势\n" +
				"- description 用中文写核心要点，可用 \\n 分隔多条要点\n" +
				"- image 没有则输出空字符串\n" +
				"- 不访问个人数据、设备数据、邮箱、飞书、微信或私有会话",
			AllowTools: true,
		}, true
	default:
		return a2aPromptProfileSpec{}, false
	}
}
