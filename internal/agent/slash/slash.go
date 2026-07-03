package slash

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mnhkahn/xiaoli/internal/agent/channel"
	"github.com/mnhkahn/xiaoli/internal/agent/model"
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

type Suggestion struct {
	Name        string
	Description string
	Kind        string
}

type ModelInfo struct {
	LLM           string
	VLLM          string
	ASR           string
	TTS           string
	ContextLength int
	MaxTokens     int
}

type ModelOption = model.Option

type Dependencies interface {
	ListSkills(ctx context.Context) ([]SkillInfo, error)
	ModelInfo() ModelInfo
	ListModels(role model.Role) []ModelOption
	UseModel(role model.Role, id string) error
	ListChannels(ctx context.Context) ([]channel.Info, error)
	LLMStats(ctx context.Context) string
	NewSession(ctx context.Context) string
	ListSessions(ctx context.Context) string
	ResumeSession(ctx context.Context, id string) string
	SessionContext(ctx context.Context, id string) string
	CompressSession(ctx context.Context) string
	ProviderBalances(ctx context.Context) map[string]string
	MemoryList(ctx context.Context) string
	MemorySave(ctx context.Context, key, value string) string
	MemoryForget(ctx context.Context, key string) string
	MemoryClear(ctx context.Context) string
	WorkflowList(ctx context.Context) string
	WorkflowRun(ctx context.Context, id string) string
	ReminderList(ctx context.Context) string
	ReminderAdd(ctx context.Context, at, text string) string
	ReminderDelete(ctx context.Context, id string) string
	MCPStatus(ctx context.Context) string
	TaskStatusList(ctx context.Context) string
	TaskStatusByID(ctx context.Context, id string) string
	TaskStatusListGrouped(ctx context.Context) string
	LogSearch(ctx context.Context, keyword string, maxLines int) string
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
		return h.status(ctx), true
	case "usage":
		return h.usage(ctx), true
	case "new":
		return h.deps.NewSession(ctx), true
	case "sessions":
		return h.deps.ListSessions(ctx), true
	case "resume":
		return h.resumeSession(ctx, cmd.Args), true
	case "session":
		return h.sessionContext(ctx, cmd.Args), true
	case "compact":
		return h.deps.CompressSession(ctx), true
	case "memory":
		return h.memory(ctx, cmd.Args), true
	case "cron":
		return h.cron(ctx, cmd.Args), true
	case "reminder":
		return h.reminder(ctx, cmd.Args), true
	case "mcp":
		return h.deps.MCPStatus(ctx), true
	case "tasks":
		return h.deps.TaskStatusListGrouped(ctx), true
	case "task":
		if cmd.Args == "" {
			return h.deps.TaskStatusListGrouped(ctx), true
		}
		return h.taskStatus(ctx, cmd.Args), true
	case "log", "logs":
		return h.deps.LogSearch(ctx, cmd.Args, 50), true
	case "help":
		return helpText(), true
	default:
		return "", false
	}
}

func (h Handler) SkillPrompt(ctx context.Context, text string) (string, bool) {
	cmd, ok := Parse(text)
	if !ok || isBuiltinCommandName(cmd.Name) {
		return "", false
	}
	skills, err := h.deps.ListSkills(ctx)
	if err != nil {
		return "", false
	}
	for _, skill := range skills {
		if strings.EqualFold(skill.Name, cmd.Name) {
			return formatSkillPrompt(skill.Name, cmd.Args), true
		}
	}
	return "", false
}

func (h Handler) Suggestions(ctx context.Context, prefix string) []Suggestion {
	prefix = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(prefix, "/")))
	var out []Suggestion
	for _, cmd := range builtinSuggestions() {
		if strings.HasPrefix(strings.ToLower(cmd.Name), prefix) {
			out = append(out, cmd)
		}
	}
	skills, err := h.deps.ListSkills(ctx)
	if err == nil {
		for _, skill := range skills {
			name := strings.TrimSpace(skill.Name)
			if name == "" || !strings.HasPrefix(strings.ToLower(name), prefix) || isBuiltinCommandName(strings.ToLower(name)) {
				continue
			}
			out = append(out, Suggestion{Name: name, Description: skill.Description, Kind: "skill"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "cmd"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func builtinSuggestions() []Suggestion {
	return []Suggestion{
		{Name: "help", Description: "显示帮助", Kind: "cmd"},
		{Name: "skills", Description: "列出可用 skills", Kind: "cmd"},
		{Name: "model", Description: "查看或切换模型", Kind: "cmd"},
		{Name: "usage", Description: "查看模型供应商用量", Kind: "cmd"},
		{Name: "status", Description: "查看状态和 LLM 统计", Kind: "cmd"},
		{Name: "sessions", Description: "列出会话", Kind: "cmd"},
		{Name: "resume", Description: "切换历史会话", Kind: "cmd"},
		{Name: "session", Description: "查看会话上下文", Kind: "cmd"},
		{Name: "new", Description: "新建会话", Kind: "cmd"},
		{Name: "compact", Description: "压缩当前会话", Kind: "cmd"},
		{Name: "memory", Description: "管理记忆", Kind: "cmd"},
		{Name: "mcp", Description: "查看 MCP 状态", Kind: "cmd"},
		{Name: "tasks", Description: "查看任务", Kind: "cmd"},
		{Name: "task", Description: "查看单个任务", Kind: "cmd"},
		{Name: "log", Description: "搜索日志", Kind: "cmd"},
		{Name: "logs", Description: "搜索日志", Kind: "cmd"},
		{Name: "reminder", Description: "管理提醒", Kind: "cmd"},
		{Name: "cron", Description: "查看定时任务", Kind: "cmd"},
		{Name: "channel", Description: "查看渠道", Kind: "cmd"},
	}
}

func isBuiltinCommandName(name string) bool {
	switch name {
	case "skills", "model", "channel", "status", "usage", "new", "sessions", "resume", "session", "compact", "memory", "cron", "reminder", "mcp", "tasks", "task", "log", "logs", "help":
		return true
	default:
		return false
	}
}

func formatSkillPrompt(name, args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		args = "请根据该 skill 的说明处理这个请求。"
	}
	return fmt.Sprintf("用户通过 Slash 命令调用了 %s skill。\n\n请先加载并遵循 %s skill 的说明来处理下面的请求；如果该 skill 要求执行命令，请按 skill 说明使用工具执行。\n\n请求：\n%s", name, name, args)
}

func (h Handler) sessionContext(ctx context.Context, id string) string {
	if id == "" {
		return "用法：/session <id>"
	}
	return h.deps.SessionContext(ctx, id)
}

func (h Handler) resumeSession(ctx context.Context, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "用法：/resume <id>"
	}
	return h.deps.ResumeSession(ctx, id)
}

func (h Handler) memory(ctx context.Context, args string) string {
	fields := strings.Fields(args)
	if len(fields) == 0 || fields[0] == "list" {
		return h.deps.MemoryList(ctx)
	}
	switch fields[0] {
	case "save":
		if len(fields) < 3 {
			return "用法：/memory save <分类> <内容>"
		}
		return h.deps.MemorySave(ctx, fields[1], strings.Join(fields[2:], " "))
	case "delete":
		if len(fields) < 2 {
			return "用法：/memory delete <分类>"
		}
		return h.deps.MemoryForget(ctx, fields[1])
	case "clear":
		return h.deps.MemoryClear(ctx)
	default:
		return "未知子命令，可用：list, save, delete, clear"
	}
}

func (h Handler) cron(ctx context.Context, args string) string {
	fields := strings.Fields(args)
	if len(fields) == 0 || fields[0] == "list" {
		return h.deps.WorkflowList(ctx)
	}
	if fields[0] == "run" {
		if len(fields) < 2 {
			return "用法：/cron run <任务ID>"
		}
		return h.deps.WorkflowRun(ctx, fields[1])
	}
	return "未知子命令，可用：list, run"
}

func (h Handler) reminder(ctx context.Context, args string) string {
	fields := strings.Fields(args)
	if len(fields) == 0 || fields[0] == "list" {
		return h.deps.ReminderList(ctx)
	}
	switch fields[0] {
	case "add":
		// /reminder add <时间> <内容>，时间为 RFC3339 或 "2006-01-02 15:04"
		rest := strings.TrimSpace(strings.TrimPrefix(args, fields[0]))
		at, text := splitReminderAddArgs(rest)
		if at == "" || text == "" {
			return "用法：/reminder add <时间> <内容>\n例：/reminder add 2026-06-25T09:00:00+08:00 交房租\n或：/reminder add \"2026-06-25 09:00\" 交房租"
		}
		return h.deps.ReminderAdd(ctx, at, text)
	case "del", "delete", "rm":
		if len(fields) < 2 {
			return "用法：/reminder del <提醒ID>"
		}
		return h.deps.ReminderDelete(ctx, fields[1])
	default:
		return "未知子命令，可用：list, add, del"
	}
}

// splitReminderAddArgs 解析 add 参数：支持引号包裹的时间（含空格），否则取第一段为时间
func splitReminderAddArgs(rest string) (at, text string) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", ""
	}
	if rest[0] == '"' {
		if end := strings.Index(rest[1:], "\""); end >= 0 {
			at = rest[1 : 1+end]
			text = strings.TrimSpace(rest[1+end+1:])
			return at, text
		}
	}
	parts := strings.SplitN(rest, " ", 2)
	at = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		text = strings.TrimSpace(parts[1])
	}
	return at, text
}

func (h Handler) taskStatus(ctx context.Context, args string) string {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return h.deps.TaskStatusList(ctx)
	}
	if fields[0] == "status" {
		if len(fields) >= 2 {
			return h.deps.TaskStatusByID(ctx, fields[1])
		}
		return h.deps.TaskStatusList(ctx)
	}
	return "用法：/task status [task_id]"
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
	b.WriteString("可用 Skills（可用 /<名称> <请求> 调用；与内置命令冲突的名称会跳过）：")
	for _, skill := range skills {
		b.WriteString("\n- ")
		if isBuiltinCommandName(strings.ToLower(skill.Name)) {
			b.WriteString(skill.Name)
			b.WriteString("（Slash 入口跳过）")
		} else {
			b.WriteString("/")
			b.WriteString(skill.Name)
		}
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
	if info.ContextLength > 0 {
		b.WriteString(fmt.Sprintf("  窗口 %dK", info.ContextLength/1024))
		if info.MaxTokens > 0 {
			b.WriteString(fmt.Sprintf(" | 输出 %d", info.MaxTokens))
		}
	}
	writeValue(&b, "VLLM", info.VLLM)
	writeValue(&b, "ASR", info.ASR)
	writeValue(&b, "TTS", info.TTS)
	return b.String()
}

func (h Handler) status(ctx context.Context) string {
	info := h.deps.ModelInfo()
	var b strings.Builder
	if info.LLM != "" {
		b.WriteString("当前模型：")
		b.WriteString(info.LLM)
		if info.ContextLength > 0 {
			fmt.Fprintf(&b, "\n窗口 %dK", info.ContextLength/1024)
			if info.MaxTokens > 0 {
				fmt.Fprintf(&b, " | 输出上限 %d", info.MaxTokens)
			}
		}
		b.WriteByte('\n')
	}
	b.WriteString(h.deps.LLMStats(ctx))
	return b.String()
}

func (h Handler) usage(ctx context.Context) string {
	balances := h.deps.ProviderBalances(ctx)
	if len(balances) == 0 {
		return "当前没有配置可查询的模型供应商用量。"
	}
	providers := make([]string, 0, len(balances))
	for name := range balances {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	var b strings.Builder
	b.WriteString("当前模型用量：")
	for _, name := range providers {
		fmt.Fprintf(&b, "\n- %s: %s", name, balances[name])
	}
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
				fmt.Fprintf(&b, " 窗口 %dK", option.ContextLength/1024)
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
	/compact    - 手动压缩当前会话的历史消息为摘要，保留最近对话
	/memory     - 管理用户记忆（/memory list 查看, /memory save <分类> <内容> 记录, /memory delete <分类> 删除, /memory clear 清空）
	/cron       - 查看和管理定时任务（/cron list 查看, /cron run <任务ID> 立即执行）
	/reminder   - 管理提醒（/reminder list 查看, /reminder add <时间> <内容> 创建, /reminder del <ID> 删除）
	/mcp        - 查看 MCP 外部服务连接状态
	/skills     - 列出所有可用技能及其版本号
	/tasks      - 查看 Task 任务面板
	/task       - 查看 Task 运行状态（/task status <id>）
	/model      - 查看或切换 LLM 模型（/model list 查看可选模型，/model use <id> 切换）
	/usage      - 查看当前配置模型供应商的用量/余额
	/channel    - 查看可用消息渠道
	/status     - 查看 LLM 调用统计
	/sessions   - 列出所有会话
	/resume     - 切换到历史会话，/resume <id>
	/session    - 查看会话上下文，/session <id>
	/new        - 新建会话
	/log        - 搜索日志，/log <关键词>
	/help       - 显示此帮助信息`
}

func AskLarkCard(question string, options []string) string {
	actions := make([]map[string]any, 0, len(options))
	for _, opt := range options {
		actions = append(actions, map[string]any{
			"tag": "button",
			"text": map[string]any{
				"tag":     "plain_text",
				"content": opt,
			},
			"value": map[string]any{
				"ask_value": opt,
			},
			"type": "default",
		})
	}

	card := map[string]any{
		"_lark_card": true,
		"config":     map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "请选择",
			},
		},
		"elements": []map[string]any{
			{
				"tag":  "div",
				"text": map[string]any{"tag": "lark_md", "content": question},
			},
			{
				"tag":     "action",
				"actions": actions,
			},
		},
	}

	data, _ := json.Marshal(card)
	return string(data)
}

func AskText(question string, options []string) string {
	var b strings.Builder
	b.WriteString(question)
	b.WriteString("\n\n请回复：")
	for i, opt := range options {
		fmt.Fprintf(&b, "\n%d. %s", i+1, opt)
	}
	return b.String()
}
