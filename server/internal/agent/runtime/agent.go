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

	agentmodel "xiaoli/server/internal/agent/model"
	agentbuiltin "xiaoli/server/internal/agent/tool/builtin"
	agentmcp "xiaoli/server/internal/agent/tool/mcp"
	agentskill "xiaoli/server/internal/agent/tool/skill"
	agentsession "xiaoli/server/internal/agent/session"
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

	msgs := make([]*schema.Message, 0, len(history)+2)
	if a.cfg.LLMPrompt != "" {
		msgs = append(msgs, schema.SystemMessage(a.cfg.LLMPrompt))
	}
	msgs = append(msgs, history...)
	msgs = append(msgs, schema.UserMessage(userText))

	einoTools := a.toolsForChat(ctx, memoryID, deviceID)

	chatModel, modelID, err := a.chatModel(ctx)
	if err != nil {
		return "", fmt.Errorf("create chat model: %w", err)
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

	return result.Content, nil
}

func (a *Agent) Generate(ctx context.Context, system, user string) (string, error) {
	msgs := make([]*schema.Message, 0, 2)
	if system != "" {
		msgs = append(msgs, schema.SystemMessage(system))
	}
	msgs = append(msgs, schema.UserMessage(user))

	einoTools := a.toolsForChat(ctx, "", "")

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

func (a *Agent) toolsForChat(_ context.Context, _ string, deviceID string) []tool.BaseTool {
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
	return einoTools
}
