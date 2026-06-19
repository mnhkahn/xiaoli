package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	agentchannel "xiaoli/server/internal/agent/channel"
	agentlark "xiaoli/server/internal/agent/channel/lark"
	agentwechat "xiaoli/server/internal/agent/channel/wechat"
	agentmodel "xiaoli/server/internal/agent/model"
	"xiaoli/server/internal/agent/slash"
	agentskill "xiaoli/server/internal/agent/tool/skill"
	agentworkflow "xiaoli/server/internal/agent/workflow"
	agentesp32 "xiaoli/server/internal/esp32"
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
	case "skills", "model", "channel", "help":
		return cmd, true
	default:
		return builtinCommand{}, false
	}
}

func (s *AdminServer) handleBuiltinCommand(ctx context.Context, source ConversationChannel, text string) (string, bool) {
	sourceType, ok := slashSourceType(source)
	if !ok {
		return "", false
	}
	return slash.NewHandler(adminSlashDeps{s: s}).Handle(ctx, sourceType, text)
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
	return slash.ModelInfo{
		LLM:  llm,
		VLLM: d.s.cfg.GoVLLMModel,
		ASR:  d.s.cfg.GoASRModel,
		TTS:  d.s.cfg.GoTTSModel,
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

func (d adminSlashDeps) LLMStats() string {
	if d.s.agent == nil {
		return "LLM agent 未初始化。"
	}
	recorder := d.s.agent.Recorder()
	if recorder == nil {
		return "LLM 统计未启用。"
	}
	return recorder.Status()
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
	defs := []agentworkflow.Definition{DefinitionChatReact()}

	studyMeta := map[string]any{
		"window":           fmt.Sprintf("%02d:00-%02d:00", s.cfg.StudyMonitorStartHour, s.cfg.StudyMonitorEndHour),
		"interval_seconds": int(defaultDuration(s.cfg.StudyMonitorInterval, 10*time.Minute).Seconds()),
		"camera_tool":      s.cfg.StudyMonitorCameraTool,
		"reminder_text":    s.cfg.StudyMonitorReminder,
		"device_ids":       s.cfg.StudyMonitorDeviceIDs,
	}
	defs = append(defs, agentworkflow.Definition{
		ID:          "study_monitor",
		Name:        "学习状态监控",
		Description: "在设定时间窗内定时调用摄像头检查学习状态，并按需发送语音提醒和飞书通知。",
		Enabled:     s.cfg.StudyMonitorEnabled,
		Trigger: agentworkflow.Trigger{
			Kind: agentworkflow.TriggerCron,
			Cron: &agentworkflow.CronSpec{
				Every:     defaultDuration(s.cfg.StudyMonitorInterval, 10*time.Minute),
				Timezone:  s.cfg.StudyMonitorTimezone,
				StartHour: s.cfg.StudyMonitorStartHour,
				EndHour:   s.cfg.StudyMonitorEndHour,
			},
		},
		Agent:    agentworkflow.AgentSpec{Name: "dispatch_agent", Mode: "react", MaxSteps: 6, Timeout: s.cfg.StudyMonitorToolTimeout + 30*time.Second},
		Metadata: studyMeta,
	})

	hour := clampInt(s.cfg.MorningGreetingHour, 0, 23, 8)
	minute := clampInt(s.cfg.MorningGreetingMinute, 0, 59, 0)
	greetingMeta := map[string]any{
		"time":       fmt.Sprintf("%02d:%02d", hour, minute),
		"text":       firstText(strings.TrimSpace(s.cfg.MorningGreetingText), "早上好。"),
		"device_ids": s.cfg.MorningGreetingDeviceIDs,
	}
	defs = append(defs, agentworkflow.Definition{
		ID:          "morning_greeting",
		Name:        "早安问候",
		Description: "每天早上固定时间向在线设备播放问候语；没有在线设备时跳过，不补播。",
		Enabled:     s.cfg.MorningGreetingEnabled,
		Trigger: agentworkflow.Trigger{
			Kind: agentworkflow.TriggerCron,
			Cron: &agentworkflow.CronSpec{
				Timezone: s.cfg.MorningGreetingTimezone,
				AtHour:   &hour,
				AtMinute: &minute,
			},
		},
		Agent:    agentworkflow.AgentSpec{Name: "dispatch_agent", Mode: "react", MaxSteps: 4, Timeout: 120 * time.Second},
		Metadata: greetingMeta,
	})
	return defs
}

func defaultDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
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
