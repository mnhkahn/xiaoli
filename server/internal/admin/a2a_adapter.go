package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
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
	agent       a2aAgentRunner
	profiles    map[string]A2AProfileConfig
	newsFetcher geekNewsFetcher
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

// newA2APipelineWithNewsFetcher enables the deterministic geek-news pipeline.
// Keeping the fetcher injectable makes the external CLI boundary testable.
func newA2APipelineWithNewsFetcher(agent a2aAgentRunner, profiles map[string]A2AProfileConfig, fetcher geekNewsFetcher) *a2aPipeline {
	return &a2aPipeline{agent: agent, profiles: profiles, newsFetcher: fetcher}
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
		if profile.Name == "geek-news" && p.newsFetcher != nil {
			reply, err := p.runGeekNews(ctx, turn, profile, req.UserText)
			if err != nil {
				return a2a.ConversationReply{}, err
			}
			return a2a.ConversationReply{Text: reply}, nil
		}
		systemPrompt := profile.SystemPrompt
		if profile.Name == "encouragement" {
			// 天气/赛事这两路数据源只能查“今天”，如果调用方传的是非当天日期
			// （补历史 / 排未来），强制在 prompt 末尾追加禁令，防止把服务运行
			// 当天的天气或赛事写进历史或未来日期的鼓励语。
			if extra := encouragementDateGuardOverride(req.UserText, time.Now()); extra != "" {
				systemPrompt = systemPrompt + "\n\n" + extra
			}
		}
		runnerReq := agentruntime.PromptProfileRequest{
			Name:           profile.Name,
			SystemPrompt:   systemPrompt,
			UserText:       req.UserText,
			ChannelName:    turn.Channel,
			SessionKey:     turn.ConversationID,
			DisableHistory: true,
			AllowTools:     profile.AllowTools,
			MaxSteps:       profile.MaxSteps,
			Model:          profile.Model,
		}
		var structuredOutput *agentruntime.PromptProfileStructuredOutput
		if profile.Name == "geek-news" {
			structuredOutput = newGeekNewsStructuredOutput()
			runnerReq.StructuredOutput = structuredOutput
		}
		reply, err := p.agent.RunPromptProfile(ctx, runnerReq)
		if err != nil {
			if profile.Name != "geek-news" || structuredOutput == nil {
				return a2a.ConversationReply{}, err
			}
			rawArguments, structuredErr, ok := structuredOutput.Failure()
			if !ok {
				return a2a.ConversationReply{}, err
			}
			errOffset := jsonSyntaxErrorOffset(structuredErr)
			logger.Infof("[A2A][geek-news][structured_output_invalid] conversation_id=%s agent_err=%v err=%v err_offset=%d args_len=%d args_near=%q args_preview=%q", turn.ConversationID, err, structuredErr, errOffset, len(rawArguments), truncateA2ALogValueAround(rawArguments, errOffset, 400), truncateA2ALogValue(rawArguments, 800))
			repaired, repairErr := p.repairGeekNewsReply(ctx, turn, profile, rawArguments, structuredErr, errOffset)
			if repairErr != nil {
				logger.Infof("[A2A][geek-news][structured_output_repair_failed] conversation_id=%s agent_err=%v structured_err=%v repair_err=%v", turn.ConversationID, err, structuredErr, repairErr)
				return a2a.ConversationReply{}, fmt.Errorf("repair invalid structured output: %w", repairErr)
			}
			logger.Infof("[A2A][geek-news][structured_output_repaired] conversation_id=%s args_len=%d reply_len=%d", turn.ConversationID, len(rawArguments), len(repaired))
			reply = repaired
		}
		if profile.Name == "geek-news" {
			if structured, ok := structuredOutput.Result(); ok {
				reply = structured
				logger.Infof("[A2A][geek-news][structured_output_ok] conversation_id=%s reply_len=%d", turn.ConversationID, len(reply))
			} else {
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
	AINews     []geekNewsItem `json:"ai_news"`
}

type geekNewsItem struct {
	Link        string `json:"link"`
	Title       string `json:"title"`
	SourceTitle string `json:"source_title"`
	Description string `json:"description"`
	Image       string `json:"image"`
	CreateTime  int64  `json:"create_time"`
}

func newGeekNewsStructuredOutput() *agentruntime.PromptProfileStructuredOutput {
	newsItem := &schema.ParameterInfo{
		Type: schema.Object,
		SubParams: map[string]*schema.ParameterInfo{
			"link":         {Type: schema.String, Desc: "新闻原始链接", Required: true},
			"title":        {Type: schema.String, Desc: "中文标题", Required: true},
			"source_title": {Type: schema.String, Desc: "源新闻标题，必须原样保留", Required: true},
			"description":  {Type: schema.String, Desc: "中文长描述", Required: true},
			"image":        {Type: schema.String, Desc: "封面图链接，没有则为空字符串", Required: true},
			"create_time":  {Type: schema.Integer, Desc: "新闻创建时间", Required: true},
		},
	}
	return agentruntime.NewPromptProfileStructuredOutput(
		"structured_output",
		"提交本次请求的最终结构化结果。完成新闻查询、翻译和整理后必须调用且只调用一次；不要在普通文本中输出 JSON。",
		map[string]*schema.ParameterInfo{
			"create_time": {Type: schema.Integer, Desc: "新闻批次创建时间", Required: true},
			"summary":     {Type: schema.String, Desc: "中文新闻总结", Required: true},
			"news":        {Type: schema.Array, ElemInfo: newsItem, Desc: "新闻列表", Required: true},
			"ai_news":     {Type: schema.Array, ElemInfo: newsItem, Desc: "AI 新闻列表", Required: true},
		},
		normalizeGeekNewsReply,
	)
}

const geekNewsJSONRepairPrompt = "你是严格的 JSON 语法修复器。\n" +
	"输入是一个 JSON 对象，包含 error、err_offset、error_near、invalid_json。\n" +
	"任务：只修复 invalid_json 的 JSON 语法，让它可以被标准 json.Unmarshal 解析。\n" +
	"硬性要求：\n" +
	"- 只输出修复后的 JSON 对象，不要 Markdown，不要代码块，不要解释\n" +
	"- 不要新增、删除、总结或改写任何新闻内容\n" +
	"- 顶层字段必须保持 create_time、news、ai_news、summary\n" +
	"- news 和 ai_news 数组每条字段必须保持 link、title、source_title、description、image、create_time\n" +
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
	if news.AINews == nil {
		news.AINews = []geekNewsItem{}
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

// encouragementDateIsToday reports whether userText's "date" field equals
// the current calendar day in Asia/Shanghai. Only a valid ISO YYYY-MM-DD
// value matching today returns true. Missing or unrecognised dates return
// false so the caller cannot accidentally combine live data with an
// historical or future encouragement.
func encouragementDateIsToday(userText string, now time.Time) bool {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	today := now.In(loc).Format("2006-01-02")
	var payload struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal([]byte(userText), &payload); err != nil {
		return false
	}
	d := strings.TrimSpace(payload.Date)
	if d == "" {
		return false
	}
	if _, err := time.ParseInLocation("2006-01-02", d, loc); err != nil {
		return false
	}
	return d == today
}

// encouragementDateGuardOverride returns extra SystemPrompt text that must
// be appended when the caller asks for a non-today date. It forbids the two
// tools whose only implementation queries "today" (maps_weather returns
// current weather; cyeam tv today lists today's matches), so they cannot
// leak service-day data into a historical or future encouragement.
func encouragementDateGuardOverride(userText string, now time.Time) string {
	if encouragementDateIsToday(userText, now) {
		return ""
	}
	return "重要覆盖规则（当 date 不是服务运行当天时生效）：\n" +
		"- 严禁调用 maps_weather MCP 工具（其只能返回当前天气，不适用于历史或未来日期）\n" +
		"- 严禁调用 cyeam tv today skill（其只能返回当天赛事，不适用于历史或未来日期）\n" +
		"- 仅使用 holiday skill 的结果，配合月周期与星期特性组织语言\n" +
		"- 不得在鼓励语中出现任何天气或比赛内容"
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
				"数据采集（必须按顺序调用，任一步失败就跳过它，不要因单步失败中断整体流程）：\n" +
				"- 必须调用 holiday skill：cyeam date holiday <date>，拿到星期几和工作日 / 休息日 / 调休补班状态\n" +
				"- 必须调用 maps_weather MCP 工具（来自高德 AMap MCP），city 固定传「北京」，拿到今日天气现象和温度（仅当 date 就是服务运行当天时才允许调用；补历史或排未来的日期不得调用）\n" +
				"- 可以调用 tv skill：cyeam tv today，拿到今日重点赛事列表（仅当 date 就是服务运行当天时才允许调用；补历史或排未来的日期不得调用）\n" +
				"生成规则：\n" +
				"- 综合以下信号组合成一句话：\n" +
				"  - holiday 结果（必用）\n" +
				"  - 天气：必须结合查询结果，只在显著时提（如高温 ≥35℃、雨雪、大风、寒潮、特别舒适），普通天气可以不提\n" +
				"  - 赛事：只在有中国队 / 世界杯淘汰赛 / NBA 总决赛这类值得说的场次时提，否则不提\n" +
				"  - 月周期：月初（1-5号）、月中（14-16号）、月末（≥25号）\n" +
				"  - 星期特性：周一鼓劲、周三过半、周五收尾\n" +
				"- 只输出一句话，不要解释，不要分段，不要输出工具调用过程\n" +
				"- 不要提具体日期，但可以说星期几\n" +
				"- 自然、真诚、有力\n" +
				"- 休息日：鼓励放松享受积蓄能量\n" +
				"- 工作日/调休补班：结合月周期、星期、天气、赛事自然组合：\n" +
				"  - 周一：新一周开始，打气鼓励\n" +
				"  - 周三：一周过半，坚持住\n" +
				"  - 周五：周末就在眼前，期待并站好最后一班岗\n" +
				"  - 月初：新的一月，设定小目标，开启新篇章\n" +
				"  - 月中：进度过半，检查完成情况，继续加油\n" +
				"  - 月末：冲刺收尾，给自己一个漂亮收尾，不留遗憾\n" +
				"  - 多个维度同时触发时自然组合（如：周一+月初、周五+月末、周三+雨天）",
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
				"- 内层 JSON 包含 news、ai_news、date；两个列表分别原样返回，供客户端的两个 Tab 使用，不得合并、截断、去重或删除\n" +
				"- 完成新闻查询、翻译和整理后，必须调用 structured_output 工具提交最终结果；不要在普通文本中输出 JSON、Markdown、代码块或解释\n" +
				"- structured_output 的顶层字段必须是 create_time、news、ai_news、summary\n" +
				"- news 与 ai_news 都是数组，每条必须包含 link、title、source_title、description、image、create_time\n" +
				"- 必须原样保留 cyeam 返回的 link、source_title、image、create_time；不得编造、替换或交换新闻记录\n" +
				"- description 使用 cyeam 返回的长描述，不要压缩成一句话；如果是英文必须翻译、补写为 300–500 字中文\n" +
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
