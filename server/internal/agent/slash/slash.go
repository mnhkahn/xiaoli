package slash

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"xiaoli/server/internal/agent/channel"
	"xiaoli/server/internal/agent/model"
)

type ctxKey string

const CtxDeviceID ctxKey = "device_id"
const CtxChannelName ctxKey = "channel_name"

func DeviceIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(CtxDeviceID).(string)
	return id
}

func ChannelNameFromContext(ctx context.Context) string {
	name, _ := ctx.Value(CtxChannelName).(string)
	return name
}

type Command struct {
	Name string
	Args string
}

type SkillInfo struct {
	Name        string
	Description string
	Version     string
}

type ModelInfo struct {
	LLM  string
	VLLM string
	ASR  string
	TTS  string
}

type ModelOption = model.Option

type Dependencies interface {
	ListSkills(ctx context.Context) ([]SkillInfo, error)
	ModelInfo() ModelInfo
	ListModels(role model.Role) []ModelOption
	UseModel(role model.Role, id string) error
	ListChannels(ctx context.Context) ([]channel.Info, error)
	LLMStats() string
	NewSession(ctx context.Context) string
	ListSessions(ctx context.Context) string
	SessionContext(ctx context.Context, id string) string
	ProviderBalances(ctx context.Context) map[string]string
}

type Handler struct {
	deps Dependencies
}

func NewHandler(deps Dependencies) Handler {
	return Handler{deps: deps}
}

func Parse(text string) (Command, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return Command{}, false
	}
	fields := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(fields) == 0 {
		return Command{}, false
	}
	return Command{
		Name: strings.ToLower(fields[0]),
		Args: strings.TrimSpace(strings.Join(fields[1:], " ")),
	}, true
}

func (h Handler) Handle(ctx context.Context, source channel.Type, text string) (string, bool) {
	if source == channel.TypeESP32 {
		return "", false
	}
	cmd, ok := Parse(text)
	if !ok {
		return "", false
	}
	switch cmd.Name {
	case "skills":
		return h.skills(ctx), true
	case "model":
		return h.model(ctx, cmd.Args), true
	case "channel":
		return h.channels(ctx), true
	case "status":
		return h.deps.LLMStats(), true
	case "new":
		return h.deps.NewSession(ctx), true
	case "sessions":
		return h.deps.ListSessions(ctx), true
	case "session":
		return h.sessionContext(ctx, cmd.Args), true
	case "help":
		return helpText(), true
	default:
		return "", false
	}
}

func (h Handler) sessionContext(ctx context.Context, id string) string {
	if id == "" {
		return "用法：/session <id>"
	}
	return h.deps.SessionContext(ctx, id)
}

func (h Handler) skills(ctx context.Context) string {
	skills, err := h.deps.ListSkills(ctx)
	if err != nil {
		return "读取 Skill 列表失败：" + err.Error()
	}
	if len(skills) == 0 {
		return "当前没有启用的 Skill。"
	}
	var b strings.Builder
	b.WriteString("可用 Skills：")
	for _, skill := range skills {
		b.WriteString("\n- ")
		b.WriteString(skill.Name)
		if skill.Version != "" {
			b.WriteString(" v")
			b.WriteString(skill.Version)
		}
		if skill.Description != "" {
			b.WriteString("：")
			b.WriteString(skill.Description)
		}
	}
	return b.String()
}

func (h Handler) model(ctx context.Context, args string) string {
	fields := strings.Fields(args)
	if len(fields) > 0 {
		switch fields[0] {
		case "list":
			return h.modelList(ctx)
		case "use":
			return h.modelUse(fields[1:])
		}
	}
	info := h.deps.ModelInfo()
	var b strings.Builder
	b.WriteString("当前模型配置：")
	writeValue(&b, "LLM", info.LLM)
	writeValue(&b, "VLLM", info.VLLM)
	writeValue(&b, "ASR", info.ASR)
	writeValue(&b, "TTS", info.TTS)
	return b.String()
}

func (h Handler) modelList(ctx context.Context) string {
	options := h.deps.ListModels(model.RoleLLM)
	current := h.deps.ModelInfo().LLM
	if len(options) == 0 {
		return "当前没有配置可切换的 LLM 模型。"
	}
	var b strings.Builder
	b.WriteString("可选 LLM 模型：")
	for i, option := range options {
		fmt.Fprintf(&b, "\n%d. %s", i+1, option.ID)
		if option.ID == current {
			b.WriteString(" 当前")
		}
		if option.MaxTokens > 0 || option.ContextLength > 0 {
			b.WriteString(" |")
			if option.ContextLength > 0 {
				fmt.Fprintf(&b, " 上下文 %dK", option.ContextLength/1024)
			}
			if option.MaxTokens > 0 {
				fmt.Fprintf(&b, " 输出 %d", option.MaxTokens)
			}
		}
	}
	balances := h.deps.ProviderBalances(ctx)
	if len(balances) > 0 {
		b.WriteString("\n\n套餐余额：")
		providers := make([]string, 0, len(balances))
		for name := range balances {
			providers = append(providers, name)
		}
		sort.Strings(providers)
		for _, name := range providers {
			fmt.Fprintf(&b, "\n- %s: %s", name, balances[name])
		}
	}
	return b.String()
}

func (h Handler) modelUse(args []string) string {
	if len(args) == 0 {
		return "用法：/model use <model-id>"
	}
	role := model.RoleLLM
	modelID := strings.TrimSpace(strings.Join(args, " "))
	if len(args) >= 2 && isModelRole(args[0]) {
		if model.Role(args[0]) != model.RoleLLM {
			return "当前只支持切换 LLM 模型。"
		}
		modelID = strings.TrimSpace(strings.Join(args[1:], " "))
	}
	if err := h.deps.UseModel(role, modelID); err != nil {
		return "切换模型失败：" + err.Error()
	}
	return "已切换 LLM 模型：" + modelID
}

func isModelRole(value string) bool {
	switch model.Role(value) {
	case model.RoleLLM, model.RoleVLLM, model.RoleASR, model.RoleTTS:
		return true
	default:
		return false
	}
}

func (h Handler) channels(ctx context.Context) string {
	channels, err := h.deps.ListChannels(ctx)
	if err != nil {
		return "读取 Channel 列表失败：" + err.Error()
	}
	if len(channels) == 0 {
		return "当前没有可用 Channel。"
	}
	var b strings.Builder
	b.WriteString("可用 Channels：")
	for _, ch := range channels {
		b.WriteString("\n- ")
		b.WriteString(ch.ID)
		b.WriteString(" [")
		b.WriteString(string(ch.Type))
		b.WriteString("]")
		if ch.Status != "" {
			b.WriteString(" ")
			b.WriteString(ch.Status)
		}
	}
	return b.String()
}

func writeValue(b *strings.Builder, name, value string) {
	if strings.TrimSpace(value) == "" {
		value = "未配置"
	}
	fmt.Fprintf(b, "\n- %s: %s", name, value)
}

func helpText() string {
	return `可用命令：
/skills     - 列出所有可用技能及其版本号
/model      - 查看或切换 LLM 模型（/model list 查看可选模型，/model use <id> 切换）
/channel    - 查看可用消息渠道
/status     - 查看 LLM 调用统计
/sessions   - 列出所有会话
/session    - 查看会话上下文，/session <id>
/new        - 新建会话
/help       - 显示此帮助信息`
}
