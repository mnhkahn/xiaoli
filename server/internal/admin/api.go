package admin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	agentchannel "github.com/mnhkahn/xiaoli/internal/agent/channel"
	agentlark "github.com/mnhkahn/xiaoli/internal/agent/channel/lark"
	agentwechat "github.com/mnhkahn/xiaoli/internal/agent/channel/wechat"
	agentmodel "github.com/mnhkahn/xiaoli/internal/agent/model"
	"github.com/mnhkahn/xiaoli/internal/agent/provider"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	"github.com/mnhkahn/xiaoli/internal/agent/slash"
	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
	agentskill "github.com/mnhkahn/xiaoli/internal/agent/tool/skill"
	agentworkflow "github.com/mnhkahn/xiaoli/internal/agent/workflow"
	agentesp32 "github.com/mnhkahn/xiaoli/server/internal/esp32"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

type BridgeClient struct {
	baseURL string
	client  *http.Client
}

type Device = agentesp32.Device

type ToolListResponse = agentesp32.ToolListResponse

type BridgeCallRequest = agentesp32.BridgeCallRequest

type BridgeCallResult = agentesp32.BridgeCallResult

func NewBridgeClient(baseURL string, client *http.Client) *BridgeClient {
	if client == nil {
		client = &http.Client{Timeout: 125 * time.Second}
	}
	return &BridgeClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (c *BridgeClient) Devices(ctx context.Context) ([]Device, error) {
	var response struct {
		Devices []Device `json:"devices"`
	}
	if err := c.getJSON(ctx, "/bridge/devices", &response); err != nil {
		return nil, err
	}
	return response.Devices, nil
}

func (c *BridgeClient) Tools(ctx context.Context, deviceID string) (ToolListResponse, error) {
	var response ToolListResponse
	path := "/bridge/tools?device_id=" + url.QueryEscape(deviceID)
	if err := c.getJSON(ctx, path, &response); err != nil {
		return ToolListResponse{}, err
	}
	return response, nil
}

func (c *BridgeClient) Call(ctx context.Context, request BridgeCallRequest) (BridgeCallResult, error) {
	var response BridgeCallResult
	if request.Arguments == nil {
		request.Arguments = map[string]any{}
	}
	if err := c.postJSON(ctx, "/bridge/call", request, &response); err != nil {
		return BridgeCallResult{}, err
	}
	return response, nil
}

func (c *BridgeClient) Speak(ctx context.Context, deviceID string, text string) (map[string]any, error) {
	request := map[string]any{"device_id": deviceID, "text": text}
	var response map[string]any
	if err := c.postJSON(ctx, "/bridge/speak", request, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *BridgeClient) StopSpeak(ctx context.Context, deviceID string) (map[string]any, error) {
	request := map[string]any{"device_id": deviceID}
	var response map[string]any
	if err := c.postJSON(ctx, "/bridge/speak/stop", request, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *BridgeClient) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, out)
}

func (c *BridgeClient) postJSON(ctx context.Context, path string, in any, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, out)
}

func (c *BridgeClient) doJSON(req *http.Request, out any) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		if message := stringValue(payload["error"]); message != "" {
			return fmt.Errorf("bridge %s failed: %s", req.URL.Path, message)
		}
		return fmt.Errorf("bridge %s failed: status %d", req.URL.Path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type ChannelType = agentchannel.Type

const (
	ChannelTypeESP32  = agentchannel.TypeESP32
	ChannelTypeLark   = agentchannel.TypeLark
	ChannelTypeWechat = agentchannel.TypeWechat
)

type ChannelCapabilities = agentchannel.Capabilities

type ChannelInfo = agentchannel.Info

type ChannelProvider = agentchannel.Provider

func (s *AdminServer) channels(ctx context.Context) ([]ChannelInfo, error) {
	return agentchannel.NewRegistry(s.channelProviders()...).List(ctx)
}

func (s *AdminServer) channelProviders() []agentchannel.Provider {
	return []agentchannel.Provider{
		deviceChannelProvider{devices: s.deviceController()},
		agentlark.Provider(agentlark.ProviderConfig{
			AppID:   s.cfg.LarkAppID,
			Enabled: s.cfg.LarkEnabled(),
		}),
		agentwechat.Provider(agentwechat.ProviderConfig{
			Enabled: s.cfg.WeChatEnabled,
			Token:   s.cfg.WeChatBotToken,
		}),
	}
}

type deviceChannelProvider struct {
	devices DeviceController
}

func (p deviceChannelProvider) ListChannels(ctx context.Context) ([]ChannelInfo, error) {
	devices, err := p.devices.Devices(ctx)
	if err != nil {
		return nil, err
	}
	channels := make([]ChannelInfo, 0, len(devices))
	for _, device := range devices {
		channels = append(channels, channelFromDevice(device))
	}
	return channels, nil
}

func channelFromDevice(device Device) ChannelInfo {
	return agentchannel.ESP32InfoFromDevice(agentchannel.DeviceInfo{
		DeviceID:     device.DeviceID,
		SessionID:    device.SessionID,
		ClientIP:     device.ClientIP,
		MCPReady:     device.MCPReady,
		ToolCount:    device.ToolCount,
		ConnectedAt:  device.ConnectedAt,
		LastActivity: device.LastActivity,
		Raw:          device,
	})
}

type builtinCommand = slash.Command

func parseBuiltinCommand(text string) (builtinCommand, bool) {
	cmd, ok := slash.Parse(text)
	if !ok {
		return builtinCommand{}, false
	}
	switch cmd.Name {
	case "skills", "model", "channel", "help", "log":
		return cmd, true
	default:
		return builtinCommand{}, false
	}
}

func (s *AdminServer) handleBuiltinCommand(ctx context.Context, source ConversationChannel, deviceID string, text string) (string, bool) {
	sourceType, ok := slashSourceType(source)
	if !ok {
		return "", false
	}
	if deviceID != "" {
		ctx = context.WithValue(ctx, slash.CtxDeviceID, deviceID)
	}
	if source != "" {
		ctx = context.WithValue(ctx, slash.CtxChannelName, string(source))
	}
	return slash.NewHandler(adminSlashDeps{s: s}).Handle(ctx, sourceType, text)
}

func (s *AdminServer) skillSlashText(ctx context.Context, source ConversationChannel, deviceID string, text string) string {
	sourceType, ok := slashSourceType(source)
	if !ok || sourceType == agentchannel.TypeESP32 {
		return text
	}
	if deviceID != "" {
		ctx = context.WithValue(ctx, slash.CtxDeviceID, deviceID)
	}
	if source != "" {
		ctx = context.WithValue(ctx, slash.CtxChannelName, string(source))
	}
	if prompt, ok := slash.NewHandler(adminSlashDeps{s: s}).SkillPrompt(ctx, text); ok {
		return prompt
	}
	return text
}

func slashSourceType(source ConversationChannel) (agentchannel.Type, bool) {
	switch source {
	case ChannelLarkText:
		return agentchannel.TypeLark, true
	case ChannelWechatText:
		return agentchannel.TypeWechat, true
	case ChannelDeviceVoice:
		return agentchannel.TypeESP32, true
	default:
		return "", false
	}
}

type adminSlashDeps struct {
	s *AdminServer
}

func (d adminSlashDeps) ListSkills(ctx context.Context) ([]slash.SkillInfo, error) {
	if len(d.s.cfg.SkillRoots) == 0 {
		return nil, nil
	}
	backend, err := agentskill.NewFileBackend(agentskill.BackendConfig{
		Roots:    d.s.cfg.SkillRoots,
		Enabled:  d.s.cfg.EnabledSkills,
		MaxBytes: d.s.cfg.SkillMaxBytes,
	})
	if err != nil {
		return nil, err
	}
	sfs := backend.ListVersions()
	out := make([]slash.SkillInfo, 0, len(sfs))
	for _, sf := range sfs {
		out = append(out, slash.SkillInfo{
			Name:        sf.Name,
			Description: sf.Description,
			Version:     sf.Version,
		})
	}
	return out, nil
}

func (d adminSlashDeps) ModelInfo() slash.ModelInfo {
	llm := d.s.cfg.GoLLMModel
	if d.s.agent != nil && d.s.agent.CurrentLLMModel() != "" {
		llm = d.s.agent.CurrentLLMModel()
	}
	ctxLen, maxTok := 0, 0
	if cfg, ok := d.s.cfg.GoLLMModelConfigs[llm]; ok {
		ctxLen = cfg.ContextLength
		maxTok = cfg.MaxTokens
	}
	return slash.ModelInfo{
		LLM:           llm,
		VLLM:          d.s.cfg.GoVLLMModel,
		ASR:           d.s.cfg.GoASRModel,
		TTS:           d.s.cfg.GoTTSModel,
		ContextLength: ctxLen,
		MaxTokens:     maxTok,
	}
}

func (d adminSlashDeps) ListModels(role agentmodel.Role) []slash.ModelOption {
	if role != agentmodel.RoleLLM || d.s.agent == nil {
		models := d.s.cfg.GoLLMModels
		if len(models) == 0 {
			models = []string{d.s.cfg.GoLLMModel}
		}
		return agentmodel.OptionsFromIDs(agentmodel.RoleLLM, models)
	}
	return d.s.agent.ListLLMModels()
}

func (d adminSlashDeps) UseModel(role agentmodel.Role, id string) error {
	if role != agentmodel.RoleLLM {
		return fmt.Errorf("only LLM model switching is supported")
	}
	if d.s.agent == nil {
		return fmt.Errorf("LLM agent is not configured")
	}
	return d.s.agent.UseLLMModel(id)
}

func (d adminSlashDeps) ListChannels(ctx context.Context) ([]agentchannel.Info, error) {
	return d.s.channels(ctx)
}

func (d adminSlashDeps) LLMStats(ctx context.Context) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	recorder := d.s.agent.Recorder()
	if recorder == nil {
		return "LLM 统计未启用。"
	}
	opts := agentruntime.StatusOptions{}
	if c := d.s.agent.CurrentContext(ctx, slash.ChannelNameFromContext(ctx), slash.DeviceIDFromContext(ctx)); c != nil {
		opts.Context = c
	}
	return recorder.Status(opts)
}

func (d adminSlashDeps) NewSession(ctx context.Context) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	deviceID := slash.DeviceIDFromContext(ctx)
	if deviceID == "" {
		return "无法识别用户。"
	}
	channelName := slash.ChannelNameFromContext(ctx)
	sessionID, err := d.s.agent.NewSession(ctx, channelName, deviceID)
	if err != nil {
		return "新建会话失败：" + err.Error()
	}
	return "✅ 已新建会话：" + sessionID
}

func (d adminSlashDeps) ListSessions(ctx context.Context) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	sm := d.s.agent.SessionManager()
	if sm == nil {
		return "会话功能未启用（需要 Redis）。"
	}
	deviceID := slash.DeviceIDFromContext(ctx)
	channelName := slash.ChannelNameFromContext(ctx)
	if deviceID == "" || channelName == "" {
		return "无法识别用户。"
	}
	sessions, err := sm.ListByChannel(ctx, channelName, deviceID)
	if err != nil {
		return "读取失败：" + err.Error()
	}
	if len(sessions) == 0 {
		return "暂无会话。"
	}
	var b strings.Builder
	b.WriteString("📋 会话列表：")
	for _, s := range sessions {
		fmt.Fprintf(&b, "\n- %s  %s  [%s]  %d条", s.ID, s.Title, s.Model, s.Count)
	}
	return b.String()
}

func (d adminSlashDeps) ResumeSession(ctx context.Context, id string) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	sm := d.s.agent.SessionManager()
	if sm == nil {
		return "会话功能未启用（需要 Redis）。"
	}
	info, err := sm.Get(ctx, id)
	if err != nil {
		return "读取失败：" + err.Error()
	}
	deviceID := slash.DeviceIDFromContext(ctx)
	if deviceID == "" || info.ChannelUser != deviceID || info.ChannelName != slash.ChannelNameFromContext(ctx) {
		return "无权访问该会话。"
	}
	return "服务端渠道暂不支持切换当前会话；可用 /session " + id + " 查看上下文。"
}

func (d adminSlashDeps) ProviderBalances(ctx context.Context) map[string]string {
	models := d.s.cfg.GoLLMModelConfigs
	if models == nil {
		return nil
	}
	items := make([]provider.ModelConfig, 0, len(models))
	for id, m := range models {
		if m.ID == "" {
			m.ID = id
		}
		items = append(items, provider.ModelConfig{ID: m.ID, BaseURL: m.BaseURL, APIKey: m.APIKey})
	}
	return provider.UsageFromModels(ctx, items)
}

func (d adminSlashDeps) SessionContext(ctx context.Context, id string) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	sm := d.s.agent.SessionManager()
	if sm == nil {
		return "会话功能未启用（需要 Redis）。"
	}
	info, err := sm.Get(ctx, id)
	if err != nil {
		return "读取失败：" + err.Error()
	}
	deviceID := slash.DeviceIDFromContext(ctx)
	if deviceID == "" || info.ChannelUser != deviceID || info.ChannelName != slash.ChannelNameFromContext(ctx) {
		return "无权访问该会话。"
	}
	msgs := sm.LoadMessages(ctx, id)
	var b strings.Builder
	fmt.Fprintf(&b, "🗂 %s（%s）\n模型：%s  消息：%d条\n━━━", info.Title, info.ID, info.Model, info.Count)
	for _, msg := range msgs {
		role := "👤"
		if msg.Role == "assistant" {
			role = "🤖"
		}
		fmt.Fprintf(&b, "\n%s %s", role, msg.Content)
	}
	return b.String()
}

func (d adminSlashDeps) CompressSession(ctx context.Context) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	deviceID := slash.DeviceIDFromContext(ctx)
	if deviceID == "" {
		return "无法识别用户。"
	}
	channelName := slash.ChannelNameFromContext(ctx)
	sm := d.s.agent.SessionManager()
	if sm == nil {
		return "会话功能未启用（需要 Redis）。"
	}
	sid, _, err := sm.GetOrCreate(ctx, channelName, deviceID, d.s.agent.CurrentLLMModel())
	if err != nil {
		return "获取会话失败：" + err.Error()
	}
	result, err := d.s.agent.CompressSession(ctx, sid)
	if err != nil {
		return "压缩失败：" + err.Error()
	}
	return "✅ " + result
}

func (d adminSlashDeps) memoryBackends(ctx context.Context) *agentbuiltin.MemoryBackends {
	deviceID := slash.DeviceIDFromContext(ctx)
	channelName := slash.ChannelNameFromContext(ctx)
	if deviceID == "" || channelName == "" || d.s.agent == nil || d.s.agent.MemoryReader() == nil {
		return nil
	}
	mem := d.s.agent.MemoryReader()
	return mem.MemoryBackends(channelName, deviceID)
}

func (d adminSlashDeps) MemoryList(ctx context.Context) string {
	b := d.memoryBackends(ctx)
	if b == nil {
		return "记忆功能未启用。"
	}
	result := make(map[string]string)
	if b.Global != nil {
		data, err := b.Global.List(ctx)
		if err != nil {
			return "读取记忆失败：" + err.Error()
		}
		for k, v := range data {
			result["[全局] "+k] = v
		}
	}
	if b.Channel != nil {
		data, err := b.Channel.List(ctx)
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
	for k := range result {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	buf.WriteString("已保存的记忆：")
	for _, k := range keys {
		fmt.Fprintf(&buf, "\n- %s：%s", k, result[k])
	}
	return buf.String()
}

func (d adminSlashDeps) MemorySave(ctx context.Context, key, value string) string {
	b := d.memoryBackends(ctx)
	if b == nil {
		return "记忆功能未启用。"
	}
	backend := b.Global
	if backend == nil {
		return "记忆功能未启用。"
	}
	if err := backend.Save(ctx, key, value); err != nil {
		return "保存失败：" + err.Error()
	}
	return fmt.Sprintf("已记住（全局）：%s → %s", key, value)
}

func (d adminSlashDeps) MemoryForget(ctx context.Context, key string) string {
	b := d.memoryBackends(ctx)
	if b == nil {
		return "记忆功能未启用。"
	}
	if b.Global != nil {
		if err := b.Global.Forget(ctx, key); err != nil {
			return "删除失败：" + err.Error()
		}
	}
	if b.Channel != nil {
		_ = b.Channel.Forget(ctx, key)
	}
	return fmt.Sprintf("已忘记：%s", key)
}

func (d adminSlashDeps) MemoryClear(ctx context.Context) string {
	b := d.memoryBackends(ctx)
	if b == nil {
		return "记忆功能未启用。"
	}
	var errs []string
	if b.Global != nil {
		if err := b.Global.Clear(ctx); err != nil {
			errs = append(errs, "全局："+err.Error())
		}
	}
	if b.Channel != nil {
		if err := b.Channel.Clear(ctx); err != nil {
			errs = append(errs, "频道："+err.Error())
		}
	}
	if len(errs) > 0 {
		return "部分清空失败：" + strings.Join(errs, "; ")
	}
	return "已清空所有记忆。"
}

func (d adminSlashDeps) WorkflowList(ctx context.Context) string {
	defs := d.s.workflowDefinitions()
	var b strings.Builder
	for _, def := range defs {
		if def.Trigger.Kind != agentworkflow.TriggerCron || def.Trigger.Cron == nil {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("定时任务：")
		}
		status := "启用"
		if !def.Enabled {
			status = "禁用"
		}
		spec := def.Trigger.Cron
		var schedule string
		if spec.Every > 0 {
			window := fmt.Sprintf("%02d:00-%02d:00", spec.StartHour, spec.EndHour)
			schedule = fmt.Sprintf("每 %s（%s %s）", spec.Every, spec.Timezone, window)
		} else if spec.AtHour != nil && spec.AtMinute != nil {
			schedule = fmt.Sprintf("每天 %02d:%02d（%s）", *spec.AtHour, *spec.AtMinute, spec.Timezone)
		}
		fmt.Fprintf(&b, "\n- %s [%s] %s", def.ID, status, schedule)
	}
	if b.Len() == 0 {
		return "没有配置的定时任务。"
	}
	return b.String()
}

func (d adminSlashDeps) WorkflowRun(ctx context.Context, id string) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	def := d.s.workflowByID(id)
	if def == nil {
		return fmt.Sprintf("未找到定时任务：%s", id)
	}
	if !def.Enabled {
		return fmt.Sprintf("定时任务 %s 已禁用，请先启用。", id)
	}
	defCopy := *def
	err := d.s.dispatchAgentRun(ctx, defCopy)
	if err != nil {
		return fmt.Sprintf("执行失败：%v", err)
	}
	return fmt.Sprintf("已执行：%s", id)
}

func (d adminSlashDeps) ReminderList(_ context.Context) string {
	reminders, err := d.s.reminderStore().Load()
	if err != nil {
		return "读取提醒失败：" + err.Error()
	}
	if len(reminders) == 0 {
		return "当前没有提醒。用 /reminder add <时间> <内容> 创建。"
	}
	var b strings.Builder
	b.WriteString("提醒列表：")
	for _, r := range reminders {
		status := "启用"
		if !r.Enabled {
			status = "禁用"
		}
		when := reminderScheduleText(r.Trigger)
		if r.IsOnceFired() {
			status = "已完成"
		}
		fmt.Fprintf(&b, "\n- [%s] %s（%s）%s", r.ID, r.Text, when, status)
	}
	return b.String()
}

// normalizeReminderChannel 将 slash channel 映射为提醒使用的渠道名
func normalizeReminderChannel(slashChannel string) string {
	switch slashChannel {
	case "lark_text":
		return "lark"
	case "wechat_text":
		return "wechat"
	case "device_voice":
		return "esp32"
	default:
		return slashChannel
	}
}

func (d adminSlashDeps) ReminderAdd(ctx context.Context, at, text string) string {
	parsed, err := parseReminderTime(at, d.s.cfg.Timezone)
	if err != nil {
		return "时间格式不对：" + err.Error() + "\n支持 RFC3339 或 \"2006-01-02 15:04\"。"
	}
	now := d.s.cfg.now()
	if parsed.Before(now) {
		return "提醒时间已过，请用将来的时间。"
	}
	channel := normalizeReminderChannel(slash.ChannelNameFromContext(ctx))
	r := agentworkflow.Reminder{
		ID:      fmt.Sprintf("rmd_%d", now.UnixNano()),
		Name:    text,
		Enabled: true,
		Action:  "speak",
		Trigger: agentworkflow.ReminderTrigger{
			Type: agentworkflow.ReminderOnce,
			At:   parsed.Format(time.RFC3339),
		},
		Text:      text,
		CreatedAt: now.Format(time.RFC3339),
		Channel:   channel,
		SenderID:  slash.DeviceIDFromContext(ctx),
	}
	if channel == "esp32" && r.SenderID != "" {
		r.Metadata = map[string]any{"device_ids": r.SenderID}
	}
	if err := d.s.reminderStore().Add(r); err != nil {
		return "保存提醒失败：" + err.Error()
	}
	return fmt.Sprintf("已创建提醒 [%s]：%s（%s）", r.ID, text, parsed.Format("2006-01-02 15:04"))
}

func (d adminSlashDeps) ReminderDelete(_ context.Context, id string) string {
	removed, err := d.s.reminderStore().Delete(id)
	if err != nil {
		return "删除提醒失败：" + err.Error()
	}
	if !removed {
		return fmt.Sprintf("未找到提醒：%s", id)
	}
	return fmt.Sprintf("已删除提醒：%s", id)
}

// parseReminderTime 解析用户输入的时间：优先 RFC3339，回退指定时区下的 "2006-01-02 15:04"
func parseReminderTime(s, timezone string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	loc := time.Local
	if timezone != "" {
		if l, err := time.LoadLocation(timezone); err == nil {
			loc = l
		}
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析 %q", s)
}

func reminderScheduleText(t agentworkflow.ReminderTrigger) string {
	switch t.Type {
	case agentworkflow.ReminderOnce:
		if parsed, err := time.Parse(time.RFC3339, t.At); err == nil {
			return parsed.Format("2006-01-02 15:04")
		}
		return t.At
	case agentworkflow.ReminderDaily:
		hour, minute := 0, 0
		if t.AtHour != nil {
			hour = *t.AtHour
		}
		if t.AtMinute != nil {
			minute = *t.AtMinute
		}
		return fmt.Sprintf("每天 %02d:%02d", hour, minute)
	case agentworkflow.ReminderInterval:
		return "每 " + t.Every
	default:
		return string(t.Type)
	}
}

func (d adminSlashDeps) MCPStatus(_ context.Context) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	statuses := d.s.agent.MCPStatus()
	if len(statuses) == 0 {
		return "没有配置 MCP 外部服务。"
	}
	var b strings.Builder
	b.WriteString("MCP 外部服务：")
	for _, s := range statuses {
		if s.Connected {
			fmt.Fprintf(&b, "\n- ✅ %s 已连接（%d 个工具）", s.URL, s.ToolCount)
		} else {
			fmt.Fprintf(&b, "\n- ❌ %s 连接失败：%s", s.URL, s.Error)
		}
	}
	return b.String()
}

func (d adminSlashDeps) TaskStatusList(_ context.Context) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	jobs := d.s.agent.TaskStatusList()
	if len(jobs) == 0 {
		return "暂无 Task 记录。"
	}
	var b strings.Builder
	b.WriteString("Task 列表（最近优先）：")
	for _, j := range jobs {
		icon := "🟢"
		switch j.Status {
		case "running":
			icon = "🔄"
		case "completed":
			icon = "✅"
		case "failed":
			icon = "❌"
		}
		fmt.Fprintf(&b, "\n%s %s [%s] %s", icon, j.ID, j.Status, j.CreatedAt.Format("15:04:05"))
	}
	return b.String()
}

func (d adminSlashDeps) TaskStatusListGrouped(_ context.Context) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	jobs := d.s.agent.TaskStatusList()
	if len(jobs) == 0 {
		return "暂无后台任务。"
	}
	groups := make(map[string][]agentbuiltin.JobSummary)
	for _, j := range jobs {
		key := j.AgentName
		if key == "" {
			key = j.AgentType
			if key == "" {
				key = "default"
			}
		}
		groups[key] = append(groups[key], j)
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("后台任务面板：")
	var totalRunning, totalTotal int
	for _, name := range names {
		items := groups[name]
		totalTotal += len(items)
		for _, j := range items {
			if j.Status == "running" {
				totalRunning++
			}
		}
	}
	fmt.Fprintf(&b, "\n%d running · %d total", totalRunning, totalTotal)
	for _, name := range names {
		items := groups[name]
		var running, completed, failed int
		for _, j := range items {
			switch j.Status {
			case "running":
				running++
			case "completed":
				completed++
			case "failed":
				failed++
			}
		}
		fmt.Fprintf(&b, "\n\n%s（%d）", name, len(items))
		if running > 0 {
			fmt.Fprintf(&b, " 运行中 %d", running)
		}
		if completed > 0 {
			fmt.Fprintf(&b, " 完成 %d", completed)
		}
		if failed > 0 {
			fmt.Fprintf(&b, " 失败 %d", failed)
		}
		for _, j := range items {
			statusIcon := "🟢"
			switch j.Status {
			case "running":
				statusIcon = "🔄"
			case "completed":
				statusIcon = "✅"
			case "failed":
				statusIcon = "❌"
			}
			fmt.Fprintf(&b, "\n  %s %s", statusIcon, j.ID)
			if j.Duration > 0 {
				fmt.Fprintf(&b, " · %s", j.Duration.Round(time.Second))
			}
			if j.Description != "" {
				fmt.Fprintf(&b, " · %s", j.Description)
			}
		}
	}
	return b.String()
}

func (d adminSlashDeps) TaskStatusByID(_ context.Context, id string) string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	job := d.s.agent.TaskStatusByID(id)
	if job == nil {
		return fmt.Sprintf("未找到任务：%s", id)
	}
	var b strings.Builder
	icon := "🟢"
	switch job.Status {
	case "running":
		icon = "🔄"
	case "completed":
		icon = "✅"
	case "failed":
		icon = "❌"
	}
	fmt.Fprintf(&b, "%s %s [%s]\n创建时间：%s", icon, job.ID, job.Status, job.CreatedAt.Format("15:04:05"))
	if job.Result != "" {
		truncated := job.Result
		if len(truncated) > 500 {
			truncated = truncated[:500] + "\n\n...（结果过长已截断，完整结果请在任务记录中查看）"
		}
		fmt.Fprintf(&b, "\n\n结果：\n%s", truncated)
	}
	if job.Error != "" {
		fmt.Fprintf(&b, "\n\n错误：\n%s", job.Error)
	}
	return b.String()
}

func (s *AdminServer) dispatchAgentRun(ctx context.Context, def agentworkflow.Definition) error {
	handlers := map[string]func(context.Context, agentworkflow.Definition, time.Time) error{
		"study_monitor":    s.runStudyMonitorOnce,
		"morning_greeting": s.runMorningGreetingOnce,
	}
	handler, ok := handlers[def.ID]
	if !ok {
		return fmt.Errorf("no handler registered for %q", def.ID)
	}
	return handler(ctx, def, s.cfg.now())
}

func (s *AdminServer) deviceController() DeviceController {
	if s.cfg.DirectDeviceServer && s.deviceHub != nil {
		return s.deviceHub
	}
	return s.bridge
}

func (s *AdminServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *AdminServer) handleXiaozhiOTA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	deviceID := strings.TrimSpace(r.Header.Get("Device-Id"))
	if deviceID != "" && s.deviceHub != nil && !s.deviceHub.deviceAllowed(deviceID) {
		http.Error(w, "device is not allowed", http.StatusForbidden)
		return
	}
	wsURL := strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/xiaozhi/v1/"
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	writeJSON(w, http.StatusOK, map[string]any{
		"server_time": map[string]any{
			"timestamp":       s.cfg.now().UnixMilli(),
			"timezone_offset": 480,
		},
		"websocket": map[string]any{
			"url":     wsURL,
			"token":   s.cfg.DeviceAuthKey,
			"version": 1,
		},
		"firmware": map[string]any{},
	})
}

func (s *AdminServer) handleXiaozhiWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.deviceHub == nil {
		http.Error(w, "direct device server is not configured", http.StatusServiceUnavailable)
		return
	}
	s.deviceHub.HandleWebSocket(w, r)
}

func (s *AdminServer) handleXiaozhiPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.deviceRegistry == nil {
		http.Error(w, "device pairing is unavailable", http.StatusServiceUnavailable)
		return
	}
	var request androidPairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
		http.Error(w, "invalid pairing request", http.StatusBadRequest)
		return
	}
	device, token, err := s.deviceRegistry.Claim(strings.TrimSpace(request.Code), request.DeviceID, request.DeviceName, request.DeviceKind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	wsURL := strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/xiaozhi/v1/"
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	writeJSON(w, http.StatusOK, map[string]any{
		"device":    map[string]any{"id": device.DeviceID, "name": device.Name, "kind": device.Kind},
		"websocket": map[string]any{"url": wsURL, "token": token, "version": 1},
	})
}

func (s *AdminServer) handleCreateDevicePairing(w http.ResponseWriter, r *http.Request, user map[string]any) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.deviceRegistry == nil {
		http.Error(w, "device pairing is unavailable", http.StatusServiceUnavailable)
		return
	}
	code, expiresAt, err := s.deviceRegistry.CreatePairing(strings.TrimSpace(stringValue(user["sub"])))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pairURL := strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/xiaozhi/pair"
	qrPayload := map[string]string{"pair_url": pairURL, "code": code}
	encodedPayload, _ := json.Marshal(qrPayload)
	png, err := qrcode.Encode(string(encodedPayload), qrcode.Medium, 320)
	if err != nil {
		http.Error(w, "generate pairing qr code failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"pair_url":   pairURL,
		"code":       code,
		"expires_at": expiresAt,
		"qr_payload": qrPayload,
		"qr_image":   "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

func (s *AdminServer) handleDeviceAudio(w http.ResponseWriter, r *http.Request) {
	if s.audioStore == nil {
		http.NotFound(w, r)
		return
	}
	id := path.Base(r.URL.Path)
	record, ok := s.audioStore.get(id, r.URL.Query().Get("token"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", record.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(record.Body)))
	_, _ = w.Write(record.Body)
}

func (s *AdminServer) handleVisionExplain(w http.ResponseWriter, r *http.Request, body []byte, contentType string, deviceID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if deviceID == "" {
		http.Error(w, "missing device-id", http.StatusBadRequest)
		return
	}
	if s.deviceHub != nil && !s.deviceHub.deviceAllowed(deviceID) {
		http.Error(w, "device is not allowed", http.StatusForbidden)
		return
	}
	if s.cfg.DeviceAuthEnabled && s.deviceHub != nil && !s.deviceHub.deviceAuthorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.deviceHub == nil || s.deviceHub.vision == nil {
		http.Error(w, "vision model is not configured", http.StatusServiceUnavailable)
		return
	}
	image, ok := s.extractVisionImage(contentType, body)
	if !ok {
		http.Error(w, "missing image", http.StatusBadRequest)
		return
	}
	if len(image.Body) > 2*1024*1024 {
		http.Error(w, "image too large", http.StatusRequestEntityTooLarge)
		return
	}
	fields := multipartFields(contentType, body)
	question := strings.TrimSpace(fields["question"])
	if question == "" {
		question = "请描述这张图片里的内容。"
	}
	s.storeVisionImage(deviceID, image.ContentType, image.Body)
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.GoVLLMTimeout+5*time.Second)
	defer cancel()
	answer, err := s.deviceHub.vision.Analyze(ctx, question, image.ContentType, image.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("vision model failed: %s", err), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"response": answer,
		"text":     answer,
	})
}

func (s *AdminServer) workflowDefinitions() []agentworkflow.Definition {
	defs := []agentworkflow.Definition{DefinitionChatReact(s.cfg.ChatReact)}
	defs = append(defs, s.cfg.Workflows...)
	return defs
}

func (s *AdminServer) workflowByID(id string) *agentworkflow.Definition {
	for _, def := range s.workflowDefinitions() {
		if def.ID == id {
			return &def
		}
	}
	return nil
}

func needsVision(text string) bool {
	text = strings.ToLower(text)
	for _, marker := range []string{"看", "看看", "照片", "图片", "图像", "摄像头", "画面", "拍", "坐姿", "学习状态", "我现在"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func extractMCPText(value any) string {
	switch v := value.(type) {
	case string:
		var parsed any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			if text := extractMCPText(parsed); text != "" {
				return text
			}
		}
		return v
	case map[string]any:
		if content, ok := v["content"].([]any); ok {
			var parts []string
			for _, item := range content {
				if m, ok := item.(map[string]any); ok {
					if text := strings.TrimSpace(stringValue(m["text"])); text != "" {
						parts = append(parts, extractMCPText(text))
					}
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
		for _, key := range []string{"response", "answer", "text", "message", "summary", "analysis", "result"} {
			if text := strings.TrimSpace(stringValue(v[key])); text != "" && text != "<nil>" {
				return text
			}
		}
	case []any:
		var parts []string
		for _, item := range v {
			if text := strings.TrimSpace(extractMCPText(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func assistantAudioSendDeadline(pacedStart time.Time, packetIndex int, frameDuration time.Duration) time.Time {
	return agentesp32.AssistantAudioSendDeadline(pacedStart, packetIndex, frameDuration)
}

func (d adminSlashDeps) LogSearch(ctx context.Context, keyword string, maxLines int) string {
	logDir := d.s.cfg.LogDir
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		return fmt.Sprintf("日志目录不存在：%s", logDir)
	}

	logFiles, err := filepath.Glob(filepath.Join(logDir, "*.log"))
	if err != nil {
		return fmt.Sprintf("查找日志文件失败：%v", err)
	}
	if len(logFiles) == 0 {
		return fmt.Sprintf("日志目录 %s 中没有 .log 文件", logDir)
	}

	sort.Slice(logFiles, func(i, j int) bool {
		fi, _ := os.Stat(logFiles[i])
		fj, _ := os.Stat(logFiles[j])
		return fi.ModTime().After(fj.ModTime())
	})

	keyword = strings.ToLower(strings.TrimSpace(keyword))
	startTime := time.Now().Add(-time.Hour)
	var results []string
	scannedCount := 0
	matchedCount := 0

	for _, logFile := range logFiles {
		if len(results) >= maxLines {
			break
		}

		file, err := os.Open(logFile)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(file)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		file.Close()

		for i := len(lines) - 1; i >= 0; i-- {
			if len(results) >= maxLines {
				break
			}

			line := lines[i]
			scannedCount++

			if logTime, ok := parseLogTime(line); ok {
				if logTime.Before(startTime) {
					continue
				}
			}

			if keyword != "" && !strings.Contains(strings.ToLower(line), keyword) {
				continue
			}

			matchedCount++
			results = append(results, line)
		}
	}

	if len(results) == 0 {
		if keyword == "" {
			return "没有找到日志"
		}
		return fmt.Sprintf("搜索关键词 %q 没有匹配的日志", keyword)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("共扫描 %d 行，匹配 %d 行，显示最近 %d 行：\n", scannedCount, matchedCount, len(results)))
	for i := len(results) - 1; i >= 0; i-- {
		b.WriteString(results[i])
		b.WriteByte('\n')
	}

	return b.String()
}

func parseLogTime(line string) (time.Time, bool) {
	if len(line) < 24 {
		return time.Time{}, false
	}

	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 4 {
		return time.Time{}, false
	}

	timeStr := parts[1] + " " + parts[2]
	t, err := time.ParseInLocation("2006/01/02 15:04:05", timeStr, time.Local)
	if err != nil {
		return time.Time{}, false
	}

	return t, true
}
