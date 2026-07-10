package admin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/mnhkahn/gogogo/logger"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	a2a "github.com/mnhkahn/xiaoli/server/internal/a2a"
)

// a2aPipeline adapts the EinoAgent's named subagent invocation to the
// a2a.ConversationPipeline interface. It routes A2A requests to the
// a2a_public_assistant subagent, never to the main agent. The internal
// session ID (a2a:<key_id>:<context_id>) is used as the sessionKey so the
// subagent's memory is scoped to the calling partner and context, never
// to a personal device or Lark/WeChat session.
type a2aPipeline struct {
	agent    a2aAgentRunner
	profiles map[string]A2AProfileConfig
}

var _ a2a.ConversationPipeline = (*a2aPipeline)(nil)

type a2aAgentRunner interface {
	RunNamedSubAgent(ctx context.Context, name string, prompt string, sessionKey string, channelName string) (string, error)
	RunPromptProfile(ctx context.Context, req agentruntime.PromptProfileRequest) (string, error)
	RunPromptProfileStream(ctx context.Context, req agentruntime.PromptProfileRequest, emit func(agentruntime.PromptProfileStreamEvent) bool) (agentruntime.PromptProfileStreamReply, error)
}

func newA2APipeline(agent a2aAgentRunner, profiles map[string]A2AProfileConfig) *a2aPipeline {
	return &a2aPipeline{agent: agent, profiles: profiles}
}

func (p *a2aPipeline) applyProfileOverrides(profile *a2aPromptProfileSpec, name string) {
	if p.profiles == nil {
		return
	}
	cfg, ok := p.profiles[name]
	if !ok {
		return
	}
	if cfg.Model != "" {
		profile.Model = cfg.Model
	}
	if cfg.AllowTools != nil {
		profile.AllowTools = *cfg.AllowTools
	}
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
		p.applyProfileOverrides(&profile, req.Profile)
		reply, err := p.agent.RunPromptProfile(ctx, agentruntime.PromptProfileRequest{
			Name:           profile.Name,
			SystemPrompt:   profile.SystemPrompt,
			UserText:       req.UserText,
			ChannelName:    turn.Channel,
			SessionKey:     turn.ConversationID,
			DisableHistory: true,
			AllowTools:     profile.AllowTools,
			MaxSteps:       profile.MaxSteps,
			Model:          profile.Model,
		})
		if err != nil {
			return a2a.ConversationReply{}, err
		}
		if profile.Name == "geek-news" {
			rawReply := reply
			reply, err = normalizeGeekNewsReply(rawReply)
			if err != nil {
				errOffset := jsonSyntaxErrorOffset(err)
				logger.Infof("[A2A][geek-news][normalize_failed] conversation_id=%s err=%v err_offset=%d reply_len=%d reply_near=%q reply_preview=%q", turn.ConversationID, err, errOffset, len(rawReply), truncateA2ALogValueAround(rawReply, errOffset, 400), truncateA2ALogValue(rawReply, 800))
				repaired, repairErr := p.repairGeekNewsReply(ctx, turn, profile, rawReply, err, errOffset)
				if repairErr != nil {
					logger.Infof("[A2A][geek-news][repair_failed] conversation_id=%s original_err=%v repair_err=%v", turn.ConversationID, err, repairErr)
					return a2a.ConversationReply{}, err
				}
				reply = repaired
			}
		}
		return a2a.ConversationReply{Text: reply}, nil
	}
	reply, err := p.agent.RunNamedSubAgent(ctx, "a2a_public_assistant", text, turn.ConversationID, turn.Channel)
	if err != nil {
		return a2a.ConversationReply{}, err
	}
	return a2a.ConversationReply{Text: reply}, nil
}

func (p *a2aPipeline) repairGeekNewsReply(ctx context.Context, turn a2a.ConversationTurn, profile a2aPromptProfileSpec, rawReply string, parseErr error, errOffset int64) (string, error) {
	payload := map[string]any{
		"error":        parseErr.Error(),
		"err_offset":   errOffset,
		"error_near":   truncateA2ALogValueAround(rawReply, errOffset, 600),
		"invalid_json": rawReply,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	reply, err := p.agent.RunPromptProfile(ctx, agentruntime.PromptProfileRequest{
		Name:           "geek-news-json-repair",
		SystemPrompt:   geekNewsJSONRepairPrompt,
		UserText:       string(body),
		ChannelName:    turn.Channel,
		SessionKey:     turn.ConversationID + ":json-repair",
		DisableHistory: true,
		AllowTools:     false,
		MaxSteps:       1,
		Model:          profile.Model,
	})
	if err != nil {
		return "", err
	}
	repaired, err := normalizeGeekNewsReply(reply)
	if err != nil {
		errOffset := jsonSyntaxErrorOffset(err)
		logger.Infof("[A2A][geek-news][repair_normalize_failed] conversation_id=%s err=%v err_offset=%d reply_len=%d reply_near=%q reply_preview=%q", turn.ConversationID, err, errOffset, len(reply), truncateA2ALogValueAround(reply, errOffset, 400), truncateA2ALogValue(reply, 800))
		return "", err
	}
	logger.Infof("[A2A][geek-news][repair_ok] conversation_id=%s original_len=%d repaired_len=%d", turn.ConversationID, len(rawReply), len(reply))
	return repaired, nil
}

func (p *a2aPipeline) RunStream(ctx context.Context, turn a2a.ConversationTurn, emit func(a2a.ConversationStreamEvent) bool) (a2a.ConversationReply, error) {
	if a2aAgentRunnerNil(p.agent) {
		return a2a.ConversationReply{}, errors.New("agent not available")
	}
	text := strings.TrimSpace(turn.Text)
	if text == "" {
		return a2a.ConversationReply{}, errors.New("empty text")
	}
	req, ok := parseA2AProfileRequest(text)
	if !ok || strings.TrimSpace(req.Profile) != "architect" {
		return a2a.ConversationReply{}, errors.New("streaming is only supported for architect profile")
	}
	profile, ok := a2aPromptProfile(req.Profile)
	if !ok {
		return a2a.ConversationReply{}, errors.New("unknown profile")
	}
	p.applyProfileOverrides(&profile, req.Profile)
	reply, err := p.agent.RunPromptProfileStream(ctx, agentruntime.PromptProfileRequest{
		Name:           profile.Name,
		SystemPrompt:   profile.SystemPrompt,
		UserText:       req.UserText,
		ChannelName:    turn.Channel,
		SessionKey:     turn.ConversationID,
		DisableHistory: true,
		AllowTools:     profile.AllowTools,
		MaxSteps:       profile.MaxSteps,
		Model:          profile.Model,
	}, func(ev agentruntime.PromptProfileStreamEvent) bool {
		return emit(a2a.ConversationStreamEvent{
			Kind:  a2a.ConversationStreamKind(ev.Kind),
			Delta: ev.Delta,
		})
	})
	if err != nil {
		return a2a.ConversationReply{}, err
	}
	return a2a.ConversationReply{Text: reply.Answer, Reasoning: reply.Reasoning}, nil
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
	Model        string
}

type geekNewsReply struct {
	CreateTime int64          `json:"create_time"`
	Summary    string         `json:"summary"`
	News       []geekNewsItem `json:"news"`
}

type geekNewsItem struct {
	Link        string `json:"link"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
	CreateTime  int64  `json:"create_time"`
}

const geekNewsJSONRepairPrompt = "你是严格的 JSON 语法修复器。\n" +
	"输入是一个 JSON 对象，包含 error、err_offset、error_near、invalid_json。\n" +
	"任务：只修复 invalid_json 的 JSON 语法，让它可以被标准 json.Unmarshal 解析。\n" +
	"硬性要求：\n" +
	"- 只输出修复后的 JSON 对象，不要 Markdown，不要代码块，不要解释\n" +
	"- 不要新增、删除、总结或改写任何新闻内容\n" +
	"- 顶层字段必须保持 create_time、news、summary\n" +
	"- news 数组每条字段必须保持 link、title、description、image、create_time\n" +
	"- 如果字符串内容里有英文双引号，必须转义为 \\\" 或替换为中文引号 “ ”\n" +
	"- 不要调用工具，不要访问外部数据"

func normalizeGeekNewsReply(reply string) (string, error) {
	var news geekNewsReply
	if err := json.Unmarshal([]byte(strings.TrimSpace(reply)), &news); err != nil {
		return "", err
	}
	if news.News == nil {
		news.News = []geekNewsItem{}
	}
	body, err := json.Marshal(news)
	if err != nil {
		return "", err
	}
	if !json.Valid(body) {
		return "", errors.New("generated geek news is invalid json")
	}
	return string(body), nil
}

func truncateA2ALogValue(value string, maxLen int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\n", "\\n"))
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "...(truncated)"
}

func truncateA2ALogValueAround(value string, offset int64, radius int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\n", "\\n"))
	if offset <= 0 || radius <= 0 {
		return truncateA2ALogValue(value, 800)
	}
	target := runeIndexForByteOffset(value, int(offset)-1)
	runes := []rune(value)
	start := target - radius
	if start < 0 {
		start = 0
	}
	end := target + radius
	if end > len(runes) {
		end = len(runes)
	}
	prefix := ""
	if start > 0 {
		prefix = "...(before)"
	}
	suffix := ""
	if end < len(runes) {
		suffix = "...(after)"
	}
	return prefix + string(runes[start:end]) + suffix
}

func runeIndexForByteOffset(value string, offset int) int {
	if offset <= 0 {
		return 0
	}
	runeIndex := 0
	for byteIndex := range value {
		if byteIndex >= offset {
			return runeIndex
		}
		runeIndex++
	}
	return len([]rune(value))
}

func jsonSyntaxErrorOffset(err error) int64 {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return syntaxErr.Offset
	}
	return 0
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
				"- 先使用 holiday skill 查询 date 对应的是工作日、休息日还是调休补班，以及星期几\n" +
				"- 解析日期判断月周期：月初（1-5号）、月中（14-16号）、月末（≥25号）\n" +
				"- 只输出一句话\n" +
				"- 不要解释，不要分段\n" +
				"- 不要提及具体日期，但可以说星期几\n" +
				"- 自然、真诚、有力\n" +
				"- 休息日：鼓励放松享受积蓄能量\n" +
				"- 工作日/调休补班：结合月周期和星期特性生成针对性鼓励：\n" +
				"  - 周一：新一周开始，打气鼓励\n" +
				"  - 周三：一周过半，坚持住\n" +
				"  - 周五：周末就在眼前，期待并站好最后一班岗\n" +
				"  - 月初：新的一月，设定小目标，开启新篇章\n" +
				"  - 月中：进度过半，检查完成情况，继续加油\n" +
				"  - 月末：冲刺收尾，给自己一个漂亮收尾，不留遗憾\n" +
				"  - 多个维度同时触发时自然组合（如：周一+月初、周五+月末）",
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
				"- 不访问个人数据、设备数据或私有会话\n" +
				"- 输出严格为 JSON 格式，包含两个字段：\n" +
				"  - reasoning：可公开返回的推理摘要、工具结果摘要、关键决策依据；不要输出隐藏思维链、密钥、个人数据或私有会话内容\n" +
				"  - answer：最终的架构建议正文",
			AllowTools: true,
			Model:      "siliconflow:qwen3.5-4b",
		}, true
	case "geek-news":
		return a2aPromptProfileSpec{
			Name: "geek-news",
			SystemPrompt: "你是 cyeam_web 的科技新闻生成器。客户端只接收 date 字段，例如 {\"date\":\"2026-06-28\"}。\n" +
				"要求：\n" +
				"- 使用 news skill 查询该 date 的完整科技新闻，命令格式优先使用 cyeam news get --date <YYYY-MM-DD>\n" +
				"- cyeam news get 返回 JSON 信封，data 字段是 JSON 字符串；必须解析 data 内层 JSON\n" +
				"- 内层 JSON 包含 news、ai_news、date；本 profile 只返回内层的 news 对象\n" +
				"- 只输出一个可被 json.Unmarshal 直接解析的 JSON 对象，不要 Markdown，不要代码块，不要解释\n" +
				"- JSON 顶层字段必须是 create_time、news、summary\n" +
				"- news 是数组，每条必须包含 link、title、description、image、create_time\n" +
				"- 必须保留 cyeam 返回的 image 字段作为封面图，不要删除、改名或编造；没有图片时才输出空字符串\n" +
				"- 必须保留 cyeam 返回的 create_time 时间字段；顶层 create_time 和每条新闻 create_time 都要返回\n" +
				"- description 使用 cyeam 返回的长描述，不要压缩成一句话；如果是英文必须翻译成中文\n" +
				"- summary 使用 cyeam 返回的 summary；如果是英文必须翻译成中文；缺失时再用中文概括最重要的 3-5 个趋势\n" +
				"- 所有标题、描述、总结内容都必须是中文\n" +
				"- 所有字符串内容里的英文双引号必须转义为 \\\"，或改用中文引号 “ ”；最终结果必须能被标准 JSON parser 解析\n" +
				"- 不访问个人数据、设备数据、邮箱、飞书、微信或私有会话",
			AllowTools: true,
		}, true
	default:
		return a2aPromptProfileSpec{}, false
	}
}
