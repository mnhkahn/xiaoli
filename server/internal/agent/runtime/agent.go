package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"

	agentchannel "xiaoli/server/internal/agent/channel"
	agentmodel "xiaoli/server/internal/agent/model"
	agentsession "xiaoli/server/internal/agent/session"
	agentbuiltin "xiaoli/server/internal/agent/tool/builtin"
	agentmcp "xiaoli/server/internal/agent/tool/mcp"
	agentskill "xiaoli/server/internal/agent/tool/skill"
)

type DeviceTools interface {
	agentmcp.DeviceToolCaller
	ToolSnapshot(deviceID string) ([]map[string]any, bool)
}

var chineseWeekday = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

type Agent struct {
	modelMu       sync.Mutex
	chatModels    map[string]*openai.ChatModel
	modelSelector *agentmodel.Selector
	memory        *Memory
	cfg           Config
	hub           DeviceTools
	extMCPs       []*agentmcp.Client
	extToolSets   [][]tool.BaseTool
	skillMW       adk.ChatModelAgentMiddleware
	recorder      *Recorder
	sessionMgr    *agentsession.Manager
	askData       map[string]*agentbuiltin.AskData
	askDataMu     sync.Mutex
}

func NewAgent(cfg Config) *Agent {
	selected := cfg.selectedLLMModelConfig()
	if selected.APIKey == "" {
		return nil
	}
	baseURL := strings.TrimSuffix(selected.BaseURL, "/chat/completions")
	baseURL = strings.TrimRight(baseURL, "/")

	ctx := context.Background()
	selector := newModelSelector(cfg)
	memory := NewRedisMemory(cfg)

	var sessionMgr *agentsession.Manager
	if memory != nil {
		sessionMgr = agentsession.NewManager(memory.client, cfg.RedisKeyPrefix)
	}

	recorder := GlobalRecorder()

	var extMCPs []*agentmcp.Client
	var extToolSets [][]tool.BaseTool
	for _, mcpURL := range cfg.ExternalMCPURLs {
		mcpURL = strings.TrimSpace(mcpURL)
		if mcpURL == "" {
			continue
		}
		client, err := agentmcp.NewClient(ctx, mcpURL)
		if err != nil {
			logger.Infof("ext MCP connect failed %s: %v", mcpURL, err)
			continue
		}
		tools, err := client.ListTools(ctx)
		if err != nil {
			logger.Infof("ext MCP list tools failed %s: %v", mcpURL, err)
			continue
		}
		extMCPs = append(extMCPs, client)
		extToolSets = append(extToolSets, tools)
		logger.Infof("ext MCP ready: %s tools=%d", mcpURL, len(tools))
	}

	var skillMW adk.ChatModelAgentMiddleware
	if len(cfg.SkillRoots) > 0 {
		backend, err := agentskill.NewFileBackend(agentskill.BackendConfig{
			Roots:    cfg.SkillRoots,
			Enabled:  cfg.EnabledSkills,
			MaxBytes: cfg.SkillMaxBytes,
		})
		if err != nil {
			logger.Infof("skill backend init failed: %v", err)
		} else if backend.Count() > 0 {
			buildSkillContent := agentskill.NewContentBuilder(agentskill.ExecConfig{
				Timeout:        cfg.SkillExecTimeout,
				MaxOutputBytes: cfg.SkillExecMaxOutputBytes,
				GlobalBinDirs:  cfg.SkillExecGlobalBinDirs,
			})
			if recorder != nil {
				orig := buildSkillContent
				buildSkillContent = func(ctx context.Context, skill einoskill.Skill, rawArgs string) (string, error) {
					skillName := skill.Name
					if skillName == "" {
						skillName = "skill"
					}
					recorder.RecordToolCall(skillName)
					result, err := orig(ctx, skill, rawArgs)
					if err != nil {
						recorder.RecordToolError(skillName)
					}
					return result, err
				}
			}
			mw, err := einoskill.NewMiddleware(ctx, &einoskill.Config{
				Backend:               backend,
				UseChinese:            true,
				CustomToolDescription: agentskill.BuildToolDescription,
				CustomToolParams:      agentskill.BuildToolParams,
				BuildContent:          buildSkillContent,
			})
			if err != nil {
				logger.Infof("skill middleware init failed: %v", err)
			} else {
				skillMW = mw
				logger.Infof("skill backend ready: roots=%v skills=%d exec_bins=%v", cfg.SkillRoots, backend.Count(), cfg.SkillExecGlobalBinDirs)
			}
		} else {
			logger.Infof("skill backend empty: roots=%v", cfg.SkillRoots)
		}
	}

	logger.Infof("eino agent ready: model=%s base=%s redis=%v extMCPs=%d skills=%v", cfg.LLMModel, baseURL, memory != nil, len(extMCPs), skillMW != nil)
	return &Agent{chatModels: map[string]*openai.ChatModel{}, modelSelector: selector, memory: memory, cfg: cfg, extMCPs: extMCPs, extToolSets: extToolSets, skillMW: skillMW, recorder: recorder, sessionMgr: sessionMgr}
}

func (a *Agent) Recorder() *Recorder {
	return a.recorder
}

func newModelSelector(cfg Config) *agentmodel.Selector {
	models := cfg.LLMModels
	if len(models) == 0 {
		models = []string{cfg.LLMModel}
	}
	llmOptions := agentmodel.OptionsFromIDs(agentmodel.RoleLLM, models)
	if len(cfg.LLMModelConfigs) > 0 {
		llmOptions = make([]agentmodel.Option, 0, len(cfg.LLMModelConfigs))
		for id, model := range cfg.LLMModelConfigs {
			option := agentmodel.Option{
				ID:            id,
				Role:          agentmodel.RoleLLM,
				DisplayName:   model.DisplayName,
				MaxTokens:     model.MaxTokens,
				ContextLength: model.ContextLength,
			}
			if idx := strings.Index(id, ":"); idx > 0 {
				option.Provider = id[:idx]
			}
			llmOptions = append(llmOptions, option)
		}
	}
	return agentmodel.NewSelector(
		map[agentmodel.Role]string{
			agentmodel.RoleLLM:  cfg.LLMModel,
			agentmodel.RoleVLLM: cfg.VLLMModel,
			agentmodel.RoleASR:  cfg.ASRModel,
			agentmodel.RoleTTS:  cfg.TTSModel,
		},
		map[agentmodel.Role][]agentmodel.Option{
			agentmodel.RoleLLM: llmOptions,
		},
	)
}

func (a *Agent) MemoryReader() MemoryReader {
	if a == nil || a.memory == nil {
		return nil
	}
	return a.memory
}

func (a *Agent) CurrentLLMModel() string {
	if a == nil || a.modelSelector == nil {
		return ""
	}
	return a.modelSelector.Current(agentmodel.RoleLLM)
}

func (a *Agent) UseLLMModel(id string) error {
	if a == nil || a.modelSelector == nil {
		return fmt.Errorf("model selector is not configured")
	}
	return a.modelSelector.Use(agentmodel.RoleLLM, id)
}

func (a *Agent) ListLLMModels() []agentmodel.Option {
	if a == nil || a.modelSelector == nil {
		return nil
	}
	return a.modelSelector.List(agentmodel.RoleLLM)
}

func (a *Agent) chatModel(ctx context.Context) (*openai.ChatModel, string, error) {
	modelID := a.CurrentLLMModel()
	if strings.TrimSpace(modelID) == "" {
		return nil, "", fmt.Errorf("LLM model is not configured")
	}
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	if model := a.chatModels[modelID]; model != nil {
		return model, modelID, nil
	}
	modelCfg := a.cfg.selectedLLMModelConfigFor(modelID)
	if modelCfg.Model == "" || modelCfg.BaseURL == "" || modelCfg.APIKey == "" {
		return nil, "", fmt.Errorf("LLM model %q is incomplete", modelID)
	}
	baseURL := strings.TrimSuffix(modelCfg.BaseURL, "/chat/completions")
	baseURL = strings.TrimRight(baseURL, "/")
	temp := float32(0.2)
	maxTokens := modelCfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 180
	}
	model, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:     baseURL,
		APIKey:      modelCfg.APIKey,
		Model:       modelCfg.Model,
		Timeout:     a.cfg.LLMTimeout,
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	})
	if err != nil {
		return nil, "", err
	}
	a.chatModels[modelID] = model
	return model, modelID, nil
}

func (a *Agent) SetDeviceTools(hub DeviceTools) {
	a.hub = hub
}

func (a *Agent) Chat(ctx context.Context, deviceID string, userText string) (string, error) {
	return a.ChatWithContext(ctx, deviceID, deviceID, userText)
}

func (a *Agent) SessionManager() *agentsession.Manager {
	return a.sessionMgr
}

func (a *Agent) storeAskData(conversationID string, d *agentbuiltin.AskData) {
	if d == nil {
		return
	}
	a.askDataMu.Lock()
	defer a.askDataMu.Unlock()
	if a.askData == nil {
		a.askData = make(map[string]*agentbuiltin.AskData)
	}
	a.askData[conversationID] = d
}

func (a *Agent) ConsumeAskData(conversationID string) *agentbuiltin.AskData {
	a.askDataMu.Lock()
	defer a.askDataMu.Unlock()
	d := a.askData[conversationID]
	delete(a.askData, conversationID)
	return d
}

func (a *Agent) CompressSession(ctx context.Context, memoryID string) (string, error) {
	if a.memory == nil {
		return "", fmt.Errorf("memory not configured")
	}
	history := a.memory.Load(ctx, memoryID)
	if len(history) < 4 {
		return "对话历史较短，无需压缩。", nil
	}
	chatModel, _, err := a.chatModel(ctx)
	if err != nil {
		return "", fmt.Errorf("get chat model: %w", err)
	}
	var oldPart, recentPart []*schema.Message
	if len(history) > 12 {
		recentPart = history[len(history)-10:]
		oldPart = history[:len(history)-10]
	} else {
		recentPart = history[len(history)-4:]
		oldPart = history[:len(history)-4]
	}
	if len(oldPart) < 2 {
		return "对话历史较短，无需压缩。", nil
	}
	promptMsgs := []*schema.Message{
		schema.SystemMessage("请用简洁的中文总结以下对话的核心内容，保留关键决策和事实。"),
	}
	promptMsgs = append(promptMsgs, oldPart...)
	summary, sumErr := chatModel.Generate(ctx, promptMsgs)
	if sumErr != nil {
		return "", fmt.Errorf("压缩失败：%w", sumErr)
	}
	if summary == nil || summary.Content == "" {
		return "", fmt.Errorf("压缩返回空结果")
	}
	updated := append(
		[]*schema.Message{schema.SystemMessage("以下是此前对话摘要：\n" + summary.Content)},
		recentPart...,
	)
	if err := a.memory.Save(ctx, memoryID, updated); err != nil {
		return "", fmt.Errorf("保存压缩结果失败：%w", err)
	}
	return fmt.Sprintf("已压缩：%d 条消息 → 摘要 + %d 条原文", len(oldPart), len(recentPart)), nil
}

func (a *Agent) NewSession(ctx context.Context, channelName, deviceID string) (string, error) {
	if a.sessionMgr == nil {
		return "", fmt.Errorf("会话功能未启用")
	}
	if channelName == "" || deviceID == "" {
		return "", fmt.Errorf("channel 和 deviceID 不能为空")
	}
	sessionID, _, err := a.sessionMgr.Create(ctx, channelName, deviceID, a.CurrentLLMModel())
	return sessionID, err
}

type ChatOptions struct {
	MaxIterations int
	Channel       string
}

func (a *Agent) ChatWithContext(ctx context.Context, conversationID string, deviceID string, userText string) (string, error) {
	return a.ChatWithContextOptions(ctx, conversationID, deviceID, userText, ChatOptions{})
}

func (a *Agent) ChatWithContextOptions(ctx context.Context, conversationID string, deviceID string, userText string, opts ChatOptions) (string, error) {
	if conversationID == "" {
		conversationID = deviceID
	}

	memoryID := conversationID
	var isNewSession bool
	var usingSession bool
	channelUser := deviceID
	channelName := opts.Channel
	if a.sessionMgr != nil && channelName != "" && channelUser != "" {
		sid, isNew, err := a.sessionMgr.GetOrCreate(ctx, channelName, channelUser, a.CurrentLLMModel())
		if err != nil {
			logger.Infof("session get/create failed: %v, fallback to conversationID", err)
		} else {
			memoryID = sid
			isNewSession = isNew
			usingSession = true
		}
		if isNewSession {
			a.sessionMgr.SetTitle(ctx, memoryID, userText)
		}
	}

	logger.Infof("Agent.Chat called: conversation=%s device=%s memory=%s text=%q", conversationID, deviceID, memoryID, userText)

	history := a.memory.Load(ctx, memoryID)

	var epochToday string
	if usingSession {
		var loc, _ = time.LoadLocation("Asia/Shanghai")
		now := time.Now().In(loc)
		today := now.Format("2006-01-02")
		last := a.sessionMgr.GetEpoch(ctx, channelName, channelUser)
		if last != "" && last != today {
			timeMsg := fmt.Sprintf("当前北京时间：%s %s %02d:%02d", today, chineseWeekday[now.Weekday()], now.Hour(), now.Minute())
			timeSysMsg := schema.SystemMessage(timeMsg)
			replaced := false
			for i, m := range history {
				if m.Role == schema.System && strings.Contains(m.Content, "当前北京时间") {
					history[i] = timeSysMsg
					replaced = true
					break
				}
			}
			if !replaced {
				history = append(history, timeSysMsg)
			}
		}
		if last != today {
			epochToday = today
		}
	}

	msgs := make([]*schema.Message, 0, len(history)+4)
	if a.cfg.LLMPrompt != "" {
		msgs = append(msgs, schema.SystemMessage(a.cfg.LLMPrompt))
	}
	msgs = append(msgs, schema.SystemMessage(a.buildEnvContext(channelName, deviceID)))
	msgs = append(msgs, schema.SystemMessage(a.toolGuide(true)))
	if memories := a.loadMemories(ctx, channelName, deviceID); memories != "" {
		msgs = append(msgs, schema.SystemMessage(memories))
	}
	msgs = append(msgs, history...)
	msgs = append(msgs, schema.UserMessage(userText))

	var askHolder *agentbuiltin.AskDataHolder
	ctx, askHolder = agentbuiltin.NewAskDataHolder(ctx)

	einoTools := a.toolsForChat(ctx, memoryID, deviceID, channelName)

	chatModel, modelID, err := a.chatModel(ctx)
	if err != nil {
		return "", fmt.Errorf("create chat model: %w", err)
	}

	modelCfg := a.cfg.selectedLLMModelConfigFor(modelID)
	if modelCfg.ContextLength > 0 {
		reserve := 8000
		threshold := modelCfg.ContextLength*75/100 - modelCfg.MaxTokens - reserve
		if threshold < 2000 {
			threshold = 2000
		}
		var histTokens int
		for _, m := range history {
			histTokens += len(m.Content) / 4
		}
		currentTokens := len(userText) / 4
		if histTokens+currentTokens > threshold {
			if currentTokens > modelCfg.ContextLength*40/100 {
				return "", fmt.Errorf("输入过长（约 %d tokens），请分段发送", currentTokens)
			}
			if len(history) > 4 && histTokens > threshold/3 {
				var oldPart, recentPart []*schema.Message
				if len(history) > 12 {
					recentPart = history[len(history)-10:]
					oldPart = history[:len(history)-10]
				} else {
					recentPart = history[len(history)-4:]
					oldPart = history[:len(history)-4]
				}
				if len(oldPart) >= 2 {
					promptMsgs := []*schema.Message{
						schema.SystemMessage("请用简洁的中文总结以下对话的核心内容，保留关键决策和事实。"),
					}
					promptMsgs = append(promptMsgs, oldPart...)
					summary, sumErr := chatModel.Generate(ctx, promptMsgs)
					if sumErr == nil && summary != nil && summary.Content != "" {
						logger.Infof("history compressed: %d hist msgs → %d chars", len(oldPart), len(summary.Content))
						history = append(
							[]*schema.Message{schema.SystemMessage("以下是此前对话摘要：\n" + summary.Content)},
							recentPart...,
						)
						histTokens = len(summary.Content) / 4
						for _, m := range recentPart {
							histTokens += len(m.Content) / 4
						}
					} else {
						logger.Infof("history compression failed: %v", sumErr)
					}
				}
			}
			for histTokens+currentTokens > threshold && len(history) > 4 {
				oldLen := len(history)
				history = history[len(history)/2:]
				histTokens = 0
				for _, m := range history {
					histTokens += len(m.Content) / 4
				}
				logger.Infof("history truncated: %d → %d messages", oldLen, len(history))
			}
			msgs = make([]*schema.Message, 0, len(history)+4)
			if a.cfg.LLMPrompt != "" {
				msgs = append(msgs, schema.SystemMessage(a.cfg.LLMPrompt))
			}
			msgs = append(msgs, schema.SystemMessage(a.buildEnvContext(channelName, deviceID)))
			msgs = append(msgs, schema.SystemMessage(a.toolGuide(true)))
			if memories := a.loadMemories(ctx, channelName, deviceID); memories != "" {
				msgs = append(msgs, schema.SystemMessage(memories))
			}
			msgs = append(msgs, history...)
			msgs = append(msgs, schema.UserMessage(userText))
		}
	}

	maxIterations := opts.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 10
	}
	agentCfg := &adk.ChatModelAgentConfig{
		Name:             "xiaoli",
		Instruction:      "",
		Model:            chatModel,
		MaxIterations:    maxIterations,
		ModelRetryConfig: newLLMRetryConfig(),
	}
	if a.skillMW != nil {
		agentCfg.Handlers = []adk.ChatModelAgentMiddleware{a.skillMW}
	}
	if len(einoTools) > 0 {
		agentCfg.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: einoTools,
			},
		}
	}

	agent, err := adk.NewChatModelAgent(ctx, agentCfg)
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent: agent,
	})

	runCtx := a.recorder.WithContext(ctx, modelID)
	events, err := runWithRetry(runCtx, runner, msgs)
	if err != nil {
		return "", fmt.Errorf("agent error: %w", err)
	}

	var result *schema.Message
	for _, event := range events {
		if event.Err != nil {
			logger.Infof("Agent.Chat event error: %v", event.Err)
			return "", fmt.Errorf("agent error: %w", event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil &&
			event.Output.MessageOutput.Message != nil &&
			event.Output.MessageOutput.Role == schema.Assistant {
			result = event.Output.MessageOutput.Message
		}
	}
	if result == nil || result.Content == "" {
		return "", fmt.Errorf("agent returned empty response")
	}

	updated := append(history,
		schema.UserMessage(userText),
		result,
	)
	if err := a.memory.Save(ctx, memoryID, updated); err != nil {
		logger.Infof("memory save failed, not updating epoch: %v", err)
	} else if epochToday != "" {
		a.sessionMgr.SetEpoch(ctx, channelName, channelUser, epochToday)
	}

	if usingSession {
		a.sessionMgr.UpdateAfterChat(ctx, memoryID, len(updated))
	}

	if askHolder != nil && askHolder.Data != nil {
		a.storeAskData(conversationID, askHolder.Data)
	}

	return result.Content, nil
}

func (a *Agent) toolGuide(interactive bool) string {
	current := a.CurrentLLMModel()
	var b strings.Builder
	b.WriteString(`=== 工具使用指引 ===
根据任务场景选择最合适的工具：

• webfetch — 获取网页内容。用于读取 URL、查看网页、获取公开信息。支持 markdown/text/html 格式。不支持需要登录的页面。
• websearch — 搜索网络。用于查询实时信息、最新新闻、事件和知识。`)
	if interactive {
		b.WriteString(`
• ask_user_question — 向用户提问。需要用户选择、确认或补充信息时使用。问题以按钮（飞书）或文字列表展示。不会阻塞等待用户回答。
• memory_save — 记住用户信息。仅在用户明确说"记一下""记住""以后按这个来"时调用。
• memory_forget — 删除一条用户记忆。
• memory_list — 列出所有用户记忆。
• task — 子代理任务。将复杂、多步骤或独立的工作委托给子代理执行。子代理当前使用的模型是 ` + current + `，有独立的会话和工具集。
• device tools — 设备端工具（如拍照），用于需要摄像头或硬件交互的场景。仅在 ESP32 设备可用。`)
	}
	b.WriteString(`
• MCP tools — 外部 MCP 协议工具，按需调用。`)
	return b.String()
}

func (a *Agent) buildEnvContext(channelName, deviceID string) string {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	var b strings.Builder
	b.WriteString("=== 环境信息 ===")
	fmt.Fprintf(&b, "\n当前时间：%s %s %02d:%02d", now.Format("2006-01-02"), chineseWeekday[now.Weekday()], now.Hour(), now.Minute())
	if deviceID != "" {
		fmt.Fprintf(&b, "\n用户标识：%s", deviceID)
	}
	if channelName != "" {
		switch channelName {
		case string(agentchannel.TypeLark):
			b.WriteString("\n渠道：飞书")
		case string(agentchannel.TypeWechat):
			b.WriteString("\n渠道：微信")
		case string(agentchannel.TypeESP32):
			b.WriteString("\n渠道：语音设备")
		default:
			fmt.Fprintf(&b, "\n渠道：%s", channelName)
		}
	}
	fmt.Fprintf(&b, "\n模型：%s", a.CurrentLLMModel())
	return b.String()
}

func (a *Agent) loadMemories(ctx context.Context, channelName, deviceID string) string {
	if a.memory == nil || channelName == "" || deviceID == "" {
		return ""
	}
	backend := agentbuiltin.NewMemoryBackend(a.memory.Client(), a.memory.Prefix(), channelName, deviceID)
	return agentbuiltin.LoadMemories(ctx, backend)
}

func (a *Agent) memoryTools(channelName, deviceID string) []tool.BaseTool {
	if a.memory == nil || channelName == "" || deviceID == "" {
		return nil
	}
	backend := agentbuiltin.NewMemoryBackend(a.memory.Client(), a.memory.Prefix(), channelName, deviceID)
	return []tool.BaseTool{
		a.WrapTool(agentbuiltin.NewMemorySaveTool(backend), "builtin"),
		a.WrapTool(agentbuiltin.NewMemoryForgetTool(backend), "builtin"),
		a.WrapTool(agentbuiltin.NewMemoryListTool(backend), "builtin"),
	}
}

func (a *Agent) Generate(ctx context.Context, system, user string) (string, error) {
	msgs := make([]*schema.Message, 0, 4)
	if system != "" {
		msgs = append(msgs, schema.SystemMessage(system))
	}
	msgs = append(msgs, schema.SystemMessage(a.buildEnvContext("", "")))
	msgs = append(msgs, schema.SystemMessage(a.toolGuide(false)))
	msgs = append(msgs, schema.UserMessage(user))

	einoTools := a.generateTools(ctx)

	chatModel, modelID, err := a.chatModel(ctx)
	if err != nil {
		return "", fmt.Errorf("create chat model: %w", err)
	}
	cfg := &adk.ChatModelAgentConfig{
		Name:             "xiaoli",
		Model:            chatModel,
		MaxIterations:    10,
		ModelRetryConfig: newLLMRetryConfig(),
	}
	if a.skillMW != nil {
		cfg.Handlers = []adk.ChatModelAgentMiddleware{a.skillMW}
	}
	if len(einoTools) > 0 {
		cfg.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: einoTools,
			},
		}
	}

	agent, err := adk.NewChatModelAgent(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	runCtx := a.recorder.WithContext(ctx, modelID)

	events, err := runWithRetry(runCtx, runner, msgs)
	if err != nil {
		return "", fmt.Errorf("agent error: %w", err)
	}

	var result string
	for _, event := range events {
		if event.Err != nil {
			logger.Infof("Agent.Generate event error: %v", event.Err)
			return "", fmt.Errorf("agent error: %w", event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil &&
			event.Output.MessageOutput.Message != nil &&
			event.Output.MessageOutput.Role == schema.Assistant {
			result = event.Output.MessageOutput.Message.Content
		}
	}
	if result == "" {
		return "", fmt.Errorf("agent returned empty response")
	}
	return result, nil
}

func (a *Agent) SubAgent(ctx context.Context, spec agentbuiltin.SubAgentSpec, prompt string) (string, error) {
	msgs := make([]*schema.Message, 0, 2)
	if spec.SystemPrompt != "" {
		msgs = append(msgs, schema.SystemMessage(spec.SystemPrompt))
	}
	msgs = append(msgs, schema.UserMessage(prompt))

	einoTools := a.subAgentTools(ctx, spec.AllowTools)

	chatModel, modelID, err := a.chatModel(ctx)
	if err != nil {
		return "", fmt.Errorf("create chat model: %w", err)
	}

	maxSteps := spec.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 10
	}
	cfg := &adk.ChatModelAgentConfig{
		Name:             spec.Name,
		Model:            chatModel,
		MaxIterations:    maxSteps,
		ModelRetryConfig: newLLMRetryConfig(),
	}
	if a.skillMW != nil {
		cfg.Handlers = []adk.ChatModelAgentMiddleware{a.skillMW}
	}
	if len(einoTools) > 0 {
		cfg.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: einoTools,
			},
		}
	}

	agent, err := adk.NewChatModelAgent(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("create subagent: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	runCtx := a.recorder.WithContext(ctx, modelID)

	events, err := runWithRetry(runCtx, runner, msgs)
	if err != nil {
		return "", fmt.Errorf("subagent error: %w", err)
	}

	var result string
	for _, event := range events {
		if event.Err != nil {
			logger.Infof("SubAgent event error: %v", event.Err)
			return "", fmt.Errorf("subagent error: %w", event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil &&
			event.Output.MessageOutput.Message != nil &&
			event.Output.MessageOutput.Role == schema.Assistant {
			result = event.Output.MessageOutput.Message.Content
		}
	}
	if result == "" {
		return "", fmt.Errorf("subagent returned empty response")
	}
	return result, nil
}

func (a *Agent) subAgentTools(ctx context.Context, allowTools bool) []tool.BaseTool {
	if !allowTools {
		return nil
	}
	var einoTools []tool.BaseTool
	for _, t := range agentbuiltin.NewTools(a.cfg.BuiltinWebFetchEnabled) {
		einoTools = append(einoTools, a.WrapTool(t, "builtin"))
	}
	einoTools = append(einoTools, a.WrapTool(agentbuiltin.NewWebSearchTool(""), "builtin"))
	for _, tools := range a.extToolSets {
		for _, t := range tools {
			einoTools = append(einoTools, a.WrapTool(t, "mcp"))
		}
	}
	return einoTools
}

func (a *Agent) generateTools(ctx context.Context) []tool.BaseTool {
	var einoTools []tool.BaseTool
	for _, t := range agentbuiltin.NewTools(a.cfg.BuiltinWebFetchEnabled) {
		einoTools = append(einoTools, a.WrapTool(t, "builtin"))
	}
	einoTools = append(einoTools, a.WrapTool(agentbuiltin.NewWebSearchTool(""), "builtin"))
	for _, tools := range a.extToolSets {
		for _, t := range tools {
			einoTools = append(einoTools, a.WrapTool(t, "mcp"))
		}
	}
	return einoTools
}

func (a *Agent) toolsForChat(_ context.Context, _ string, deviceID string, channelName string) []tool.BaseTool {
	var einoTools []tool.BaseTool
	for _, t := range agentbuiltin.NewTools(a.cfg.BuiltinWebFetchEnabled) {
		einoTools = append(einoTools, a.WrapTool(t, "builtin"))
	}
	einoTools = append(einoTools, a.WrapTool(agentbuiltin.NewWebSearchTool(""), "builtin"))
	if a.hub != nil && deviceID != "" {
		if rawTools, ok := a.hub.ToolSnapshot(deviceID); ok {
			for _, t := range agentmcp.NewDeviceTools(deviceID, rawTools, a.hub) {
				einoTools = append(einoTools, a.WrapTool(t, "mcp"))
			}
		}
	}
	for _, tools := range a.extToolSets {
		for _, t := range tools {
			einoTools = append(einoTools, a.WrapTool(t, "mcp"))
		}
	}
	einoTools = append(einoTools, a.WrapTool(
		agentbuiltin.NewTaskTool(
			agentbuiltin.DefaultSubAgents(),
			func(ctx context.Context, spec agentbuiltin.SubAgentSpec, prompt string) (string, error) {
				return a.SubAgent(ctx, spec, prompt)
			},
		), "builtin"))
	einoTools = append(einoTools, a.WrapTool(agentbuiltin.NewAskUserQuestionTool(), "builtin"))
	einoTools = append(einoTools, a.memoryTools(channelName, deviceID)...)
	return einoTools
}
