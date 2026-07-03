package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	agentchannel "github.com/mnhkahn/xiaoli/internal/agent/channel"
	"github.com/mnhkahn/xiaoli/internal/agent/localapp"
	agentmodel "github.com/mnhkahn/xiaoli/internal/agent/model"
	"github.com/mnhkahn/xiaoli/internal/agent/provider"
	"github.com/mnhkahn/xiaoli/internal/agent/runlog"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	agentsession "github.com/mnhkahn/xiaoli/internal/agent/session"
	"github.com/mnhkahn/xiaoli/internal/agent/slash"
	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
	agentskill "github.com/mnhkahn/xiaoli/internal/agent/tool/skill"
	agentworkflow "github.com/mnhkahn/xiaoli/internal/agent/workflow"
)

type tuiSlashDeps struct {
	app              *localapp.App
	currentSessionID string
	setSession       func(string)
}

func (d *tuiSlashDeps) ListSkills(context.Context) ([]slash.SkillInfo, error) {
	if d == nil || d.app == nil || len(d.app.Runtime.SkillRoots) == 0 {
		return nil, nil
	}
	backend, err := agentskill.NewFileBackend(agentskill.BackendConfig{
		Roots:    d.app.Runtime.SkillRoots,
		Enabled:  d.app.Runtime.EnabledSkills,
		MaxBytes: d.app.Runtime.SkillMaxBytes,
	})
	if err != nil {
		return nil, err
	}
	skills := backend.ListVersions()
	out := make([]slash.SkillInfo, 0, len(skills))
	for _, skill := range skills {
		out = append(out, slash.SkillInfo{
			Name:        skill.Name,
			Description: skill.Description,
			Version:     skill.Version,
		})
	}
	return out, nil
}

func (d *tuiSlashDeps) ModelInfo() slash.ModelInfo {
	if d == nil || d.app == nil || d.app.Agent == nil {
		return slash.ModelInfo{}
	}
	llm := d.app.Agent.CurrentLLMModel()
	cfg := d.app.Runtime.LLMModelConfigs[llm]
	return slash.ModelInfo{
		LLM:           llm,
		VLLM:          d.app.Runtime.VLLMModel,
		ASR:           d.app.Runtime.ASRModel,
		TTS:           d.app.Runtime.TTSModel,
		ContextLength: cfg.ContextLength,
		MaxTokens:     cfg.MaxTokens,
	}
}

func (d *tuiSlashDeps) ListModels(role agentmodel.Role) []slash.ModelOption {
	if role != agentmodel.RoleLLM || d == nil || d.app == nil || d.app.Agent == nil {
		return nil
	}
	return d.app.Agent.ListLLMModels()
}

func (d *tuiSlashDeps) UseModel(role agentmodel.Role, id string) error {
	if role != agentmodel.RoleLLM {
		return fmt.Errorf("only LLM model switching is supported")
	}
	if d == nil || d.app == nil || d.app.Agent == nil {
		return fmt.Errorf("LLM agent is not configured")
	}
	return d.app.Agent.UseLLMModel(id)
}

func (d *tuiSlashDeps) ListChannels(context.Context) ([]agentchannel.Info, error) {
	return []agentchannel.Info{{
		ID:          channelName + ":" + channelUser,
		Type:        agentchannel.TypeLark,
		DisplayName: "Xiaoli TUI",
		Status:      "local",
		DeviceID:    channelUser,
		Capabilities: agentchannel.Capabilities{
			Text:  true,
			Tools: true,
		},
	}}, nil
}

func (d *tuiSlashDeps) LLMStats(ctx context.Context) string {
	if d == nil || d.app == nil || d.app.Agent == nil || d.app.Agent.Recorder() == nil {
		return "LLM 统计未启用。"
	}
	opts := agentruntime.StatusOptions{}
	if c := d.app.Agent.CurrentContext(ctx, channelName, channelUser); c != nil {
		opts.Context = c
	}
	return d.app.Agent.Recorder().Status(opts)
}

func (d *tuiSlashDeps) NewSession(ctx context.Context) string {
	if d == nil || d.app == nil || d.app.Agent == nil {
		return "LLM agent 未初始化。"
	}
	sessionID, err := d.app.Agent.NewSession(ctx, channelName, channelUser)
	if err != nil {
		return "新建会话失败：" + err.Error()
	}
	d.setActiveSession(sessionID)
	return "已新建会话：" + sessionID
}

func (d *tuiSlashDeps) ListSessions(ctx context.Context) string {
	sm := d.sessionManager()
	if sm == nil {
		return "会话功能未启用。"
	}
	sessions, err := sm.ListByChannel(ctx, channelName, channelUser)
	if err != nil {
		return "读取失败：" + err.Error()
	}
	if len(sessions) == 0 {
		return "暂无会话。"
	}
	var b strings.Builder
	b.WriteString("会话列表：")
	for _, s := range sessions {
		current := ""
		if s.ID == d.currentSessionID {
			current = " 当前"
		}
		fmt.Fprintf(&b, "\n- %s  %s  [%s]  %d条%s", s.ID, s.Title, s.Model, s.Count, current)
	}
	return b.String()
}

func (d *tuiSlashDeps) ResumeSession(ctx context.Context, id string) string {
	sm := d.sessionManager()
	if sm == nil {
		return "会话功能未启用。"
	}
	info, err := sm.Get(ctx, id)
	if err != nil {
		return "读取失败：" + err.Error()
	}
	if info.ChannelName != channelName || info.ChannelUser != channelUser {
		return "无权访问该会话。"
	}
	d.setActiveSession(id)
	return fmt.Sprintf("已切换到会话：%s  %s", info.ID, info.Title)
}

func (d *tuiSlashDeps) SessionContext(ctx context.Context, id string) string {
	sm := d.sessionManager()
	if sm == nil {
		return "会话功能未启用。"
	}
	info, err := sm.Get(ctx, id)
	if err != nil {
		return "读取失败：" + err.Error()
	}
	if info.ChannelName != channelName || info.ChannelUser != channelUser {
		return "无权访问该会话。"
	}
	msgs := d.loadSessionMessages(ctx, id)
	var b strings.Builder
	fmt.Fprintf(&b, "%s（%s）\n模型：%s  消息：%d条", info.Title, info.ID, info.Model, info.Count)
	for _, msg := range msgs {
		if msg == nil || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		role := string(msg.Role)
		if role == "" {
			role = "unknown"
		}
		fmt.Fprintf(&b, "\n%s: %s", role, msg.Content)
	}
	return b.String()
}

func (d *tuiSlashDeps) CompressSession(ctx context.Context) string {
	if d == nil || d.app == nil || d.app.Agent == nil {
		return "LLM agent 未初始化。"
	}
	sessionID := d.currentSessionID
	if sessionID == "" {
		sm := d.sessionManager()
		if sm != nil {
			sessionID = sm.GetChannelSession(ctx, channelName, channelUser)
		}
	}
	if sessionID == "" {
		return "暂无当前会话。"
	}
	result, err := d.app.Agent.CompressSession(ctx, sessionID)
	if err != nil {
		return "压缩失败：" + err.Error()
	}
	return result
}

func (d *tuiSlashDeps) ProviderBalances(ctx context.Context) map[string]string {
	if d == nil || d.app == nil {
		return nil
	}
	models := make([]provider.ModelConfig, 0, len(d.app.Runtime.LLMModelConfigs))
	for id, model := range d.app.Runtime.LLMModelConfigs {
		if model.ID == "" {
			model.ID = id
		}
		models = append(models, provider.ModelConfig{ID: model.ID, BaseURL: model.BaseURL, APIKey: model.APIKey})
	}
	return provider.UsageFromModels(ctx, models)
}

func (d *tuiSlashDeps) MemoryList(ctx context.Context) string {
	backends := d.memoryBackends()
	if backends == nil {
		return "记忆功能未启用。"
	}
	result := make(map[string]string)
	if backends.Global != nil {
		data, err := backends.Global.List(ctx)
		if err != nil {
			return "读取记忆失败：" + err.Error()
		}
		for k, v := range data {
			result["[全局] "+k] = v
		}
	}
	if backends.Channel != nil {
		data, err := backends.Channel.List(ctx)
		if err != nil {
			return "读取记忆失败：" + err.Error()
		}
		for k, v := range data {
			result["[频道] "+k] = v
		}
	}
	if len(result) == 0 {
		return "目前没有保存的记忆。"
	}
	keys := make([]string, 0, len(result))
	for key := range result {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("已保存的记忆：")
	for _, key := range keys {
		fmt.Fprintf(&b, "\n- %s：%s", key, result[key])
	}
	return b.String()
}

func (d *tuiSlashDeps) MemorySave(ctx context.Context, key, value string) string {
	backends := d.memoryBackends()
	if backends == nil || backends.Global == nil {
		return "记忆功能未启用。"
	}
	if err := backends.Global.Save(ctx, key, value); err != nil {
		return "保存失败：" + err.Error()
	}
	return fmt.Sprintf("已记住（全局）：%s -> %s", key, value)
}

func (d *tuiSlashDeps) MemoryForget(ctx context.Context, key string) string {
	backends := d.memoryBackends()
	if backends == nil {
		return "记忆功能未启用。"
	}
	if backends.Global != nil {
		if err := backends.Global.Forget(ctx, key); err != nil {
			return "删除失败：" + err.Error()
		}
	}
	if backends.Channel != nil {
		_ = backends.Channel.Forget(ctx, key)
	}
	return "已忘记：" + key
}

func (d *tuiSlashDeps) MemoryClear(ctx context.Context) string {
	backends := d.memoryBackends()
	if backends == nil {
		return "记忆功能未启用。"
	}
	var errs []string
	if backends.Global != nil {
		if err := backends.Global.Clear(ctx); err != nil {
			errs = append(errs, "全局："+err.Error())
		}
	}
	if backends.Channel != nil {
		if err := backends.Channel.Clear(ctx); err != nil {
			errs = append(errs, "频道："+err.Error())
		}
	}
	if len(errs) > 0 {
		return "部分清空失败：" + strings.Join(errs, "; ")
	}
	return "已清空所有记忆。"
}

func (d *tuiSlashDeps) WorkflowList(context.Context) string {
	return "本地 TUI 没有后台调度器；一次性提醒请用 /reminder list 或 /reminder add <时间> <内容> 管理本地 reminders.json。"
}

func (d *tuiSlashDeps) WorkflowRun(context.Context, string) string {
	return "本地 TUI 没有后台调度器，不能执行 /cron run。"
}

func (d *tuiSlashDeps) ReminderList(context.Context) string {
	store := d.reminderStore()
	if store == nil {
		return "提醒功能未初始化。"
	}
	reminders, err := store.Load()
	if err != nil {
		return "读取提醒失败：" + err.Error()
	}
	if len(reminders) == 0 {
		return "当前没有提醒。用 /reminder add <时间> <内容> 创建。"
	}
	var b strings.Builder
	b.WriteString("本地提醒：")
	for _, r := range reminders {
		status := "启用"
		if !r.Enabled {
			status = "禁用"
		}
		if r.FiredAt != "" {
			status = "已完成"
		}
		fmt.Fprintf(&b, "\n- [%s] %s（%s）%s", r.ID, r.Text, reminderTimeText(r.Trigger), status)
	}
	return b.String()
}

func (d *tuiSlashDeps) ReminderAdd(_ context.Context, at, text string) string {
	store := d.reminderStore()
	if store == nil {
		return "提醒功能未初始化。"
	}
	parsed, err := parseLocalReminderTime(at)
	if err != nil {
		return "时间格式不对：" + err.Error() + "\n支持 RFC3339 或 \"2006-01-02 15:04\"。"
	}
	now := time.Now()
	if parsed.Before(now) {
		return "提醒时间已过，请用将来的时间。"
	}
	r := agentworkflow.Reminder{
		ID:      fmt.Sprintf("rmd_%d", now.UnixNano()),
		Name:    text,
		Enabled: true,
		Action:  "notify",
		Trigger: agentworkflow.ReminderTrigger{
			Type:     agentworkflow.ReminderOnce,
			At:       parsed.Format(time.RFC3339),
			Timezone: "Asia/Shanghai",
		},
		Text:      text,
		Channel:   channelName,
		SenderID:  channelUser,
		CreatedAt: now.Format(time.RFC3339),
	}
	if err := store.Add(r); err != nil {
		return "保存提醒失败：" + err.Error()
	}
	return fmt.Sprintf("已创建本地提醒 [%s]：%s（%s）", r.ID, text, parsed.Format("2006-01-02 15:04"))
}

func (d *tuiSlashDeps) ReminderDelete(_ context.Context, id string) string {
	store := d.reminderStore()
	if store == nil {
		return "提醒功能未初始化。"
	}
	removed, err := store.Delete(id)
	if err != nil {
		return "删除提醒失败：" + err.Error()
	}
	if !removed {
		return "未找到提醒：" + id
	}
	return "已删除提醒：" + id
}

func (d *tuiSlashDeps) MCPStatus(context.Context) string {
	if d == nil || d.app == nil || d.app.Agent == nil {
		return "LLM agent 未初始化。"
	}
	statuses := d.app.Agent.MCPStatus()
	if len(statuses) == 0 {
		return "没有配置 MCP 外部服务。"
	}
	var b strings.Builder
	b.WriteString("MCP 外部服务：")
	for _, status := range statuses {
		if status.Connected {
			fmt.Fprintf(&b, "\n- up %s（%d 个工具）", status.URL, status.ToolCount)
		} else {
			fmt.Fprintf(&b, "\n- down %s：%s", status.URL, status.Error)
		}
	}
	return b.String()
}

func (d *tuiSlashDeps) TaskStatusList(context.Context) string {
	if d == nil || d.app == nil || d.app.Agent == nil {
		return "LLM agent 未初始化。"
	}
	jobs := d.app.Agent.TaskStatusList()
	if len(jobs) == 0 {
		return "暂无 Task 记录。"
	}
	var b strings.Builder
	b.WriteString("Task 列表：")
	for _, job := range jobs {
		fmt.Fprintf(&b, "\n- %s [%s] %s", job.ID, job.Status, job.CreatedAt.Format("15:04:05"))
	}
	return b.String()
}

func (d *tuiSlashDeps) TaskStatusByID(_ context.Context, id string) string {
	if d == nil || d.app == nil || d.app.Agent == nil {
		return "LLM agent 未初始化。"
	}
	job := d.app.Agent.TaskStatusByID(id)
	if job == nil {
		return "未找到任务：" + id
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s]\n创建时间：%s", job.ID, job.Status, job.CreatedAt.Format("15:04:05"))
	if job.Duration > 0 {
		fmt.Fprintf(&b, "\n耗时：%s", job.Duration.Round(time.Second))
	}
	if job.Result != "" {
		result := job.Result
		if len(result) > 500 {
			result = result[:500] + "\n\n...（已截断）"
		}
		fmt.Fprintf(&b, "\n\n结果：\n%s", result)
	}
	if job.Error != "" {
		fmt.Fprintf(&b, "\n\n错误：\n%s", job.Error)
	}
	return b.String()
}

func (d *tuiSlashDeps) TaskStatusListGrouped(ctx context.Context) string {
	return d.TaskStatusList(ctx)
}

func (d *tuiSlashDeps) LogSearch(ctx context.Context, keyword string, maxLines int) string {
	if d == nil || d.app == nil || d.app.Agent == nil {
		return "LLM agent 未初始化。"
	}
	if maxLines <= 0 {
		maxLines = 50
	}
	args := parseLogArgs(keyword)
	sessionID := d.currentSessionID
	if args.All {
		sessionID = ""
	} else if sessionID == "" {
		if sm := d.sessionManager(); sm != nil {
			sessionID = sm.GetChannelSession(ctx, channelName, channelUser)
		}
	}
	if sessionID == "" && !args.All {
		return "暂无当前会话。先发送一条消息，或用 /sessions 和 /resume <id> 切换历史会话。"
	}

	keyword = strings.TrimSpace(args.Keyword)
	if selectedSession, remaining, ok := d.extractLogSession(ctx, keyword); ok {
		sessionID = selectedSession
		keyword = remaining
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	var lines []string
	for _, line := range d.logLines(ctx, sessionID, args.All) {
		lower := strings.ToLower(line)
		if args.Errors && !strings.Contains(lower, "failed") && !strings.Contains(lower, "error") && !strings.Contains(lower, "错误") {
			continue
		}
		if args.Tools && !strings.Contains(lower, "agent.tool.") {
			continue
		}
		if keyword == "" || strings.Contains(lower, keyword) {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		if keyword == "" {
			return "当前会话暂无本地日志。"
		}
		return fmt.Sprintf("当前会话没有匹配 %q 的本地日志。", keyword)
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	var b strings.Builder
	if args.All {
		b.WriteString("本地日志：全部会话")
	} else {
		fmt.Fprintf(&b, "本地日志：%s", sessionID)
	}
	if keyword != "" {
		fmt.Fprintf(&b, "\n关键词：%s", keyword)
	}
	for _, line := range lines {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	return b.String()
}

func (d *tuiSlashDeps) sessionManager() agentsession.Store {
	if d == nil || d.app == nil || d.app.Agent == nil {
		return nil
	}
	return d.app.Agent.SessionManager()
}

func (d *tuiSlashDeps) setActiveSession(id string) {
	if d != nil && d.setSession != nil {
		d.setSession(id)
	}
}

func (d *tuiSlashDeps) memoryBackends() *agentbuiltin.MemoryBackends {
	if d == nil || d.app == nil || d.app.Agent == nil || d.app.Agent.MemoryReader() == nil {
		return nil
	}
	return d.app.Agent.MemoryReader().MemoryBackends(channelName, channelUser)
}

func (d *tuiSlashDeps) loadSessionMessages(ctx context.Context, id string) []*schema.Message {
	if d == nil || d.app == nil || d.app.Agent == nil || d.app.Agent.MemoryReader() == nil {
		return nil
	}
	raw, err := d.app.Agent.MemoryReader().LoadRaw(ctx, id)
	if err != nil {
		return nil
	}
	var msgs []*schema.Message
	if err := json.Unmarshal(raw.Raw, &msgs); err != nil {
		return nil
	}
	return msgs
}

type logArgs struct {
	Keyword string
	All     bool
	Tools   bool
	Errors  bool
}

func parseLogArgs(args string) logArgs {
	var out logArgs
	var parts []string
	for _, field := range strings.Fields(args) {
		switch field {
		case "--all", "-a":
			out.All = true
		case "--tool", "--tools":
			out.Tools = true
		case "--error", "--errors":
			out.Errors = true
		default:
			parts = append(parts, field)
		}
	}
	out.Keyword = strings.Join(parts, " ")
	return out
}

func (d *tuiSlashDeps) extractLogSession(ctx context.Context, args string) (sessionID, remaining string, ok bool) {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return "", "", false
	}
	sm := d.sessionManager()
	if sm == nil {
		return "", "", false
	}
	info, err := sm.Get(ctx, fields[0])
	if err != nil || info.ChannelName != channelName || info.ChannelUser != channelUser {
		return "", "", false
	}
	return fields[0], strings.TrimSpace(strings.Join(fields[1:], " ")), true
}

func (d *tuiSlashDeps) logLines(ctx context.Context, sessionID string, all bool) []string {
	if !all {
		return d.sessionLogLines(ctx, sessionID)
	}
	sm := d.sessionManager()
	if sm == nil {
		return nil
	}
	sessions, err := sm.ListByChannel(ctx, channelName, channelUser)
	if err != nil {
		return nil
	}
	var lines []string
	for _, session := range sessions {
		for _, line := range d.sessionLogLines(ctx, session.ID) {
			lines = append(lines, session.ID+" "+line)
		}
	}
	sort.SliceStable(lines, func(i, j int) bool {
		return lines[i] < lines[j]
	})
	return lines
}

func (d *tuiSlashDeps) sessionLogLines(ctx context.Context, sessionID string) []string {
	var lines []string
	if d.app.RunLogDir != "" {
		events, err := runlog.ReadSession(d.app.RunLogDir, sessionID, 0)
		if err == nil {
			for _, event := range events {
				lines = append(lines, formatRunEvent(event.Timestamp, event.Type, event.Data))
			}
		}
	}
	for _, msg := range d.loadSessionMessages(ctx, sessionID) {
		if msg == nil || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		lines = append(lines, formatMessageLine(msg))
	}
	sort.SliceStable(lines, func(i, j int) bool {
		return lines[i] < lines[j]
	})
	return lines
}

func (d *tuiSlashDeps) reminderStore() *agentworkflow.ReminderStore {
	if d == nil || d.app == nil || d.app.Config.DataDir == "" {
		return nil
	}
	return agentworkflow.NewReminderStore(filepath.Join(d.app.Config.DataDir, "state", "reminders.json"))
}

func parseLocalReminderTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.Local
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析 %q", s)
}

func reminderTimeText(trigger agentworkflow.ReminderTrigger) string {
	if trigger.Type == agentworkflow.ReminderOnce {
		if parsed, err := time.Parse(time.RFC3339, trigger.At); err == nil {
			return parsed.Local().Format("2006-01-02 15:04")
		}
		return trigger.At
	}
	return string(trigger.Type)
}

func formatRunEvent(ts time.Time, typ string, data any) string {
	var b strings.Builder
	b.WriteString(formatLogTime(ts))
	b.WriteString(" event ")
	b.WriteString(typ)
	if data != nil {
		if raw, err := json.Marshal(data); err == nil && len(raw) > 0 && string(raw) != "null" {
			b.WriteString(" ")
			b.WriteString(truncateLogText(string(raw), 300))
		}
	}
	return b.String()
}

func formatMessageLine(msg *schema.Message) string {
	ts := time.Time{}
	if msg.Extra != nil {
		if value, ok := msg.Extra["timestamp"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, value); err == nil {
				ts = parsed
			}
		}
	}
	return fmt.Sprintf("%s message %s %s", formatLogTime(ts), msg.Role, truncateLogText(msg.Content, 300))
}

func formatLogTime(ts time.Time) string {
	if ts.IsZero() {
		return "---- --:--:--"
	}
	return ts.Local().Format("01-02 15:04:05")
}

func truncateLogText(text string, maxRunes int) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "..."
}
