package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	agentchannel "github.com/mnhkahn/xiaoli/internal/agent/channel"
	agentmodel "github.com/mnhkahn/xiaoli/internal/agent/model"
	agentsession "github.com/mnhkahn/xiaoli/internal/agent/session"
	agentbuiltin "github.com/mnhkahn/xiaoli/internal/agent/tool/builtin"
	agentmcp "github.com/mnhkahn/xiaoli/internal/agent/tool/mcp"
	agentskill "github.com/mnhkahn/xiaoli/internal/agent/tool/skill"
	agentevent "github.com/mnhkahn/xiaoli/internal/event"
)

type DeviceTools interface {
	agentmcp.DeviceToolCaller
	ToolSnapshot(deviceID string) ([]map[string]any, bool)
}

var chineseWeekday = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

var A2AAllowedMCPServers = map[string]bool{
	"CYEAM": true,
}

type MCPEndpointStatus struct {
	URL       string
	Connected bool
	ToolCount int
	Error     string
}

type Agent struct {
	modelMu          sync.Mutex
	chatModels       map[string]*openai.ChatModel
	modelSelector    *agentmodel.Selector
	memory           *Memory
	cfg              Config
	hub              DeviceTools
	extMCPs          []*agentmcp.Client
	extToolSets      [][]tool.BaseTool
	extMCPNames      []string
	extMCPStatus     []MCPEndpointStatus
	skillMW          adk.ChatModelAgentMiddleware // 普通 channel 用
	a2aSkillMW       adk.ChatModelAgentMiddleware // A2A channel 用（skill 白名单）
	recorder         *Recorder
	sessionMgr       agentsession.Store
	askData          map[string]*agentbuiltin.AskData
	askDataMu        sync.Mutex
	toolUseConfirms  map[string][]*agentbuiltin.PendingToolUseConfirm
	toolUseConfirmMu sync.Mutex
	commitRequests   map[string]*agentbuiltin.CommitRequest
	commitRequestMu  sync.Mutex
	taskTool         *agentbuiltin.TaskTool
	eventBus         agentevent.Publisher
	channelSendTool  tool.InvokableTool
	fileWriteRoots   []string
	agentFileRoots   []string
	visionAnalyzer   agentbuiltin.VisionAnalyzer
	recentImages     agentbuiltin.RecentImageStore
}

const defaultAgentMaxIterations = 200

const maxExecutionChecklistContinuations = 6

type executionChecklistContinuationKey struct{}

// noopEventPublisher is a no-op event publisher for backward compatibility
type noopEventPublisher struct{}

func (n noopEventPublisher) Publish(ctx context.Context, e agentevent.Event) error {
	return nil
}

func (a *Agent) MCPStatus() []MCPEndpointStatus {
	return a.extMCPStatus
}

func (a *Agent) TaskStatusList() []agentbuiltin.JobSummary {
	if a.taskTool == nil {
		return nil
	}
	return a.taskTool.ListJobs()
}

func (a *Agent) TaskStatusByID(id string) *agentbuiltin.BackgroundJob {
	if a.taskTool == nil {
		return nil
	}
	return a.taskTool.QueryJob(id)
}

func NewAgent(cfg Config, eventBus agentevent.Publisher) *Agent {
	if eventBus == nil {
		eventBus = noopEventPublisher{}
	}
	selected := cfg.selectedLLMModelConfig()
	if selected.APIKey == "" {
		return nil
	}
	baseURL := strings.TrimSuffix(selected.BaseURL, "/chat/completions")
	baseURL = strings.TrimRight(baseURL, "/")

	ctx := context.Background()
	selector := newModelSelector(cfg)
	memory := NewMemory(cfg)

	var sessionMgr agentsession.Store
	if memory != nil {
		if strings.EqualFold(strings.TrimSpace(cfg.StorageBackend), storageBackendLocal) {
			sessionMgr = agentsession.NewLocalManager(memory.localDataDir)
		} else if memory.client != nil {
			sessionMgr = agentsession.NewManager(memory.client, cfg.RedisKeyPrefix)
		}
	}

	recorder := GlobalRecorder()

	var extMCPs []*agentmcp.Client
	var extToolSets [][]tool.BaseTool
	var extMCPNames []string
	var extMCPStatus []MCPEndpointStatus
	for _, ep := range cfg.ExternalMCPEndpoints {
		client, err := agentmcp.NewClient(ctx, agentmcp.AuthConfig{
			URL:          ep.URL,
			APIKey:       ep.APIKey,
			Auth:         ep.Auth,
			HeaderName:   ep.HeaderN,
			TokenURL:     ep.TokenURL,
			ClientID:     ep.ClientID,
			ClientSecret: ep.ClientSecret,
			RefreshToken: ep.RefreshToken,
			Scope:        ep.Scope,
		})
		if err != nil {
			logger.Infof("ext MCP connect failed %s: %v", ep.URL, err)
			extMCPStatus = append(extMCPStatus, MCPEndpointStatus{URL: ep.URL, Connected: false, Error: err.Error()})
			continue
		}
		tools, err := client.ListTools(ctx)
		if err != nil {
			logger.Infof("ext MCP list tools failed %s: %v", ep.URL, err)
			extMCPStatus = append(extMCPStatus, MCPEndpointStatus{URL: ep.URL, Connected: false, Error: err.Error()})
			continue
		}
		extMCPs = append(extMCPs, client)
		extToolSets = append(extToolSets, tools)
		extMCPNames = append(extMCPNames, ep.Name)
		extMCPStatus = append(extMCPStatus, MCPEndpointStatus{URL: ep.URL, Connected: true, ToolCount: len(tools)})
		logger.Infof("ext MCP ready: %s tools=%d", ep.URL, len(tools))
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
	a2aSkillMW := newA2ASkillMiddleware(ctx, cfg, recorder)

	a := &Agent{chatModels: map[string]*openai.ChatModel{}, modelSelector: selector, memory: memory, cfg: cfg, extMCPs: extMCPs, extToolSets: extToolSets, extMCPNames: extMCPNames, extMCPStatus: extMCPStatus, skillMW: skillMW, a2aSkillMW: a2aSkillMW, recorder: recorder, sessionMgr: sessionMgr}

	agentRegistry := agentbuiltin.NewAgentRegistry()
	agentRoots := cfg.AgentFileRoots
	if len(agentRoots) == 0 {
		agentRoots = agentbuiltin.FileAgentRoots()
	}
	agentRegistry.LoadAgentFiles(agentRoots)
	a.eventBus = eventBus
	taskTool := agentbuiltin.NewTaskTool(
		agentRegistry.ListSpecs(),
		func(ctx context.Context, spec agentbuiltin.SubAgentSpec, rt *agentbuiltin.SubAgentRuntime, prompt string) (string, error) {
			return a.SubAgent(ctx, spec, *rt, prompt)
		},
		eventBus,
	)
	taskTool.SetInjectFn(func(ctx context.Context, taskID, state, content string) error {
		job := a.taskTool.QueryJob(taskID)
		if job == nil || job.ParentSession == "" || a.memory == nil {
			return nil
		}
		injectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		history := a.memory.Load(injectCtx, job.ParentSession)
		resultMsg := fmt.Sprintf("<task id=\"%s\" state=\"%s\">\n%s\n</task>", taskID, state, content)
		updated := append(history, schema.SystemMessage(resultMsg))
		return a.memory.Save(injectCtx, job.ParentSession, updated)
	})
	if len(cfg.TaskAllowedRoots) > 0 {
		taskTool.SetAllowedRoots(cfg.TaskAllowedRoots)
	}
	a.taskTool = taskTool

	logger.Infof("eino agent ready: model=%s base=%s redis=%v extMCPs=%d skills=%v taskTool=%v", cfg.LLMModel, baseURL, memory != nil, len(extMCPs), skillMW != nil, taskTool != nil)
	return a
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

func newA2ASkillMiddleware(ctx context.Context, cfg Config, recorder *Recorder) adk.ChatModelAgentMiddleware {
	if len(cfg.SkillRoots) == 0 || len(cfg.A2AAllowedSkills) == 0 {
		return nil
	}
	a2aBackend, err := agentskill.NewFileBackend(agentskill.BackendConfig{
		Roots:    cfg.SkillRoots,
		Enabled:  cfg.A2AAllowedSkills,
		MaxBytes: cfg.SkillMaxBytes,
	})
	if err != nil {
		logger.Infof("A2A skill backend init failed: %v", err)
		return nil
	}
	if a2aBackend.Count() == 0 {
		logger.Infof("A2A skill backend empty: roots=%v skills=%v", cfg.SkillRoots, cfg.A2AAllowedSkills)
		return nil
	}
	a2aMw, err := einoskill.NewMiddleware(ctx, &einoskill.Config{
		Backend:               a2aBackend,
		UseChinese:            true,
		CustomToolDescription: agentskill.BuildToolDescription,
		CustomToolParams:      agentskill.BuildToolParams,
		BuildContent:          newA2ASkillContentBuilder(cfg, recorder),
	})
	if err != nil {
		logger.Infof("A2A skill middleware init failed: %v", err)
		return nil
	}
	logger.Infof("A2A skill backend ready: skills=%v", cfg.A2AAllowedSkills)
	return a2aMw
}

func newA2ASkillContentBuilder(cfg Config, recorder *Recorder) func(context.Context, einoskill.Skill, string) (string, error) {
	build := agentskill.NewContentBuilder(agentskill.ExecConfig{
		Timeout:        cfg.SkillExecTimeout,
		MaxOutputBytes: cfg.SkillExecMaxOutputBytes,
		GlobalBinDirs:  cfg.SkillExecGlobalBinDirs,
	})
	if recorder == nil {
		return build
	}
	return func(ctx context.Context, skill einoskill.Skill, rawArgs string) (string, error) {
		skillName := skill.Name
		if skillName == "" {
			skillName = "skill"
		}
		recorder.RecordToolCall(skillName)
		st := traceFromContext(ctx)
		step := 0
		start := time.Now()
		if st != nil {
			step = st.nextToolStep()
			logTraceToolStart(ctx, step, skillName, "skill", rawArgs)
		}
		result, err := build(ctx, skill, rawArgs)
		if err != nil {
			recorder.RecordToolError(skillName)
		}
		if st != nil {
			logTraceToolEnd(ctx, step, skillName, "skill", len(result), time.Since(start), err, result)
		}
		return result, err
	}
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
	return a.chatModelForID(ctx, "")
}

func (a *Agent) chatModelForID(ctx context.Context, modelID string) (*openai.ChatModel, string, error) {
	if modelID == "" {
		modelID = a.CurrentLLMModel()
	}
	if strings.TrimSpace(modelID) == "" {
		return nil, "", fmt.Errorf("LLM model is not configured")
	}
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	if model := a.chatModels[modelID]; model != nil {
		return model, modelID, nil
	}
	// 显式指定的模型必须在配置中存在，避免 typo 静默 fallback 到错误的 provider
	if _, ok := a.cfg.LLMModelConfigs[modelID]; !ok && modelID != a.CurrentLLMModel() {
		return nil, "", fmt.Errorf("LLM model %q is not configured", modelID)
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
		maxTokens = 4096
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

func (a *Agent) SetVisionTools(vision agentbuiltin.VisionAnalyzer, store agentbuiltin.RecentImageStore) {
	a.visionAnalyzer = vision
	a.recentImages = store
}

// ChannelSendersConfig holds the senders for each channel type
type ChannelSendersConfig struct {
	Lark         agentchannel.Sender
	ESP32        agentchannel.Sender
	WeChat       agentchannel.Sender
	AllowedRoots []string // Trusted directories for file operations
}

type PromptProfileRequest struct {
	Name             string
	SystemPrompt     string
	UserText         string
	ChannelName      string
	SessionKey       string
	DisableHistory   bool
	AllowTools       bool
	StructuredOutput *PromptProfileStructuredOutput
	MaxSteps         int
	Model            string // 可选：强制使用指定模型，为空则用默认
}

// PromptProfileStructuredOutput describes a request-scoped final output tool.
// The runtime exposes it as one generic tool and stores its validated result.
type PromptProfileStructuredOutput struct {
	ToolName        string
	ToolDescription string
	Params          map[string]*schema.ParameterInfo
	Normalize       func(string) (string, error)

	mu         sync.Mutex
	result     string
	failureRaw string
	failureErr error
}

func NewPromptProfileStructuredOutput(name, description string, params map[string]*schema.ParameterInfo, normalize func(string) (string, error)) *PromptProfileStructuredOutput {
	return &PromptProfileStructuredOutput{
		ToolName:        name,
		ToolDescription: description,
		Params:          params,
		Normalize:       normalize,
	}
}

func (s *PromptProfileStructuredOutput) Result() (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, s.result != ""
}

// Failure returns the raw arguments and validation error from the latest
// failed structured output attempt. Callers may use it for diagnostics or a
// profile-specific repair path after the agent run fails.
func (s *PromptProfileStructuredOutput) Failure() (string, error, bool) {
	if s == nil {
		return "", nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failureRaw, s.failureErr, s.failureErr != nil
}

func (s *PromptProfileStructuredOutput) setResult(result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = result
	s.failureRaw = ""
	s.failureErr = nil
}

func (s *PromptProfileStructuredOutput) setFailure(raw string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = ""
	s.failureRaw = raw
	s.failureErr = err
}

// Capture validates, normalizes, and stores one structured output attempt.
// The unmodified arguments are retained when validation fails so the caller
// can report the exact model output or route it through a repair workflow.
func (s *PromptProfileStructuredOutput) Capture(argumentsInJSON string) (string, error) {
	var canonical bytes.Buffer
	if err := json.Compact(&canonical, []byte(argumentsInJSON)); err != nil {
		wrapped := fmt.Errorf("invalid structured output JSON: %w", err)
		s.setFailure(argumentsInJSON, wrapped)
		return "", wrapped
	}
	result := canonical.String()
	if s.Normalize != nil {
		var err error
		result, err = s.Normalize(result)
		if err != nil {
			wrapped := fmt.Errorf("invalid structured output: %w", err)
			s.setFailure(argumentsInJSON, wrapped)
			return "", wrapped
		}
	}
	s.setResult(result)
	return result, nil
}

type promptProfileStructuredOutputTool struct {
	output *PromptProfileStructuredOutput
}

func newPromptProfileStructuredOutputTool(output *PromptProfileStructuredOutput) *promptProfileStructuredOutputTool {
	return &promptProfileStructuredOutputTool{output: output}
}

func (t *promptProfileStructuredOutputTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        t.output.ToolName,
		Desc:        t.output.ToolDescription,
		ParamsOneOf: schema.NewParamsOneOfByParams(t.output.Params),
	}, nil
}

func (t *promptProfileStructuredOutputTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if _, err := t.output.Capture(argumentsInJSON); err != nil {
		return "", err
	}
	return "Structured output captured successfully.", nil
}

type PromptProfileStreamKind string

const (
	PromptProfileStreamReasoningDelta PromptProfileStreamKind = "reasoning_delta"
	PromptProfileStreamAnswerDelta    PromptProfileStreamKind = "answer_delta"
)

type PromptProfileStreamEvent struct {
	Kind  PromptProfileStreamKind
	Delta string
}

type PromptProfileStreamReply struct {
	Answer    string
	Reasoning string
}

// SetChannelSenders sets up the channel_send tool with the provided senders
func (a *Agent) SetChannelSenders(cfg ChannelSendersConfig) {
	a.channelSendTool = agentbuiltin.NewChannelSendTool(agentbuiltin.ChannelSendConfig{
		Lark:         cfg.Lark,
		ESP32:        cfg.ESP32,
		WeChat:       cfg.WeChat,
		AllowedRoots: cfg.AllowedRoots,
	})
	a.fileWriteRoots = append([]string(nil), cfg.AllowedRoots...)
}

// SetFileWriteRoots enables file_write without requiring a channel sender.
func (a *Agent) SetFileWriteRoots(roots []string) {
	a.fileWriteRoots = append([]string(nil), roots...)
}

// SetAgentFileRoots enables structured code file tools for trusted workspace roots.
func (a *Agent) SetAgentFileRoots(roots []string) {
	a.agentFileRoots = append([]string(nil), roots...)
}

func (a *Agent) Chat(ctx context.Context, deviceID string, userText string) (string, error) {
	return a.ChatWithContext(ctx, deviceID, deviceID, userText)
}

func (a *Agent) SessionManager() agentsession.Store {
	return a.sessionMgr
}

func (a *Agent) storeAskData(conversationID string, d *agentbuiltin.AskData) {
	if d == nil {
		return
	}
	logger.Infof("ask data stored: conversation=%s question_len=%d options=%d", conversationID, len(d.Question), len(d.Options))
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
	if d != nil {
		logger.Infof("ask data consumed: conversation=%s question_len=%d options=%d", conversationID, len(d.Question), len(d.Options))
	}
	return d
}

func (a *Agent) storeCommitRequest(conversationID string, request *agentbuiltin.CommitRequest) {
	if request == nil {
		return
	}
	a.commitRequestMu.Lock()
	defer a.commitRequestMu.Unlock()
	if a.commitRequests == nil {
		a.commitRequests = make(map[string]*agentbuiltin.CommitRequest)
	}
	a.commitRequests[conversationID] = request
}

func (a *Agent) ConsumeCommitRequest(conversationID string) *agentbuiltin.CommitRequest {
	a.commitRequestMu.Lock()
	defer a.commitRequestMu.Unlock()
	request := a.commitRequests[conversationID]
	delete(a.commitRequests, conversationID)
	return request
}

func (a *Agent) storeToolUseConfirm(ctx context.Context, conversationID string, d *agentbuiltin.PendingToolUseConfirm) {
	if d == nil {
		return
	}
	if strings.TrimSpace(d.ConversationID) == "" {
		d.ConversationID = conversationID
	}
	logger.Infof("tool use confirm stored: conversation=%s session=%s channel=%s device=%s tool=%s tool_use_id=%s options=%d", conversationID, d.SessionID, d.ChannelName, d.DeviceID, d.ToolName, d.ToolUseID, len(d.Options))
	a.toolUseConfirmMu.Lock()
	if a.toolUseConfirms == nil {
		a.toolUseConfirms = make(map[string][]*agentbuiltin.PendingToolUseConfirm)
	}
	a.toolUseConfirms[conversationID] = appendPendingToolUseConfirm(a.toolUseConfirms[conversationID], d)
	if strings.TrimSpace(d.SessionID) != "" && d.SessionID != conversationID {
		a.toolUseConfirms[d.SessionID] = appendPendingToolUseConfirm(a.toolUseConfirms[d.SessionID], d)
	}
	a.toolUseConfirmMu.Unlock()
	_ = a.publishToolUseConfirmEvent(ctx, d)
}

// StoreToolUseConfirmForTest stores a pending tool confirmation for tests that
// need to exercise UI/channel consumption without running a model.
func (a *Agent) StoreToolUseConfirmForTest(ctx context.Context, conversationID string, d *agentbuiltin.PendingToolUseConfirm) {
	a.storeToolUseConfirm(ctx, conversationID, d)
}

func (a *Agent) publishToolUseConfirmEvent(ctx context.Context, d *agentbuiltin.PendingToolUseConfirm) error {
	if d == nil {
		return nil
	}
	requestID := strings.TrimSpace(d.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(d.ToolUseID)
	}
	input := map[string]string{}
	if strings.EqualFold(d.ToolName, "bash") && strings.TrimSpace(d.BashCommand) != "" {
		input["command"] = d.BashCommand
	}
	sessionID := strings.TrimSpace(d.SessionID)
	return publishRunEvent(ctx, a.eventBus, agentevent.TypePermissionAsked, sessionID, agentevent.PermissionAskedData{
		RequestID:   requestID,
		Action:      "use_tool",
		Resource:    d.ToolName,
		SessionID:   sessionID,
		ToolName:    d.ToolName,
		ToolUseID:   d.ToolUseID,
		Question:    d.Question,
		Options:     append([]string(nil), d.Options...),
		Input:       input,
		BashHash:    d.BashHash,
		ChannelName: d.ChannelName,
		DeviceID:    d.DeviceID,
	})
}

func (a *Agent) ConsumeToolUseConfirm(conversationID string) *agentbuiltin.PendingToolUseConfirm {
	a.toolUseConfirmMu.Lock()
	defer a.toolUseConfirmMu.Unlock()
	queue := a.toolUseConfirms[conversationID]
	var d *agentbuiltin.PendingToolUseConfirm
	if len(queue) > 0 {
		d = queue[0]
		queue = queue[1:]
		if len(queue) == 0 {
			delete(a.toolUseConfirms, conversationID)
		} else {
			a.toolUseConfirms[conversationID] = queue
		}
	}
	if d != nil {
		if strings.TrimSpace(d.ConversationID) != "" {
			a.removeToolUseConfirmLocked(d.ConversationID, d)
		}
		if strings.TrimSpace(d.SessionID) != "" {
			a.removeToolUseConfirmLocked(d.SessionID, d)
		}
		logger.Infof("tool use confirm consumed: key=%s conversation=%s session=%s tool=%s tool_use_id=%s options=%d", conversationID, d.ConversationID, d.SessionID, d.ToolName, d.ToolUseID, len(d.Options))
	} else {
		keys := make([]string, 0, len(a.toolUseConfirms))
		for key := range a.toolUseConfirms {
			keys = append(keys, key)
		}
		logger.Infof("tool use confirm consume miss: key=%s pending_keys=%v", conversationID, keys)
	}
	return d
}

func appendPendingToolUseConfirm(queue []*agentbuiltin.PendingToolUseConfirm, d *agentbuiltin.PendingToolUseConfirm) []*agentbuiltin.PendingToolUseConfirm {
	if d == nil {
		return queue
	}
	for _, existing := range queue {
		if samePendingToolUseConfirm(existing, d) {
			return queue
		}
	}
	return append(queue, d)
}

func (a *Agent) removeToolUseConfirmLocked(key string, d *agentbuiltin.PendingToolUseConfirm) {
	if strings.TrimSpace(key) == "" || d == nil {
		return
	}
	queue := a.toolUseConfirms[key]
	if len(queue) == 0 {
		return
	}
	filtered := queue[:0]
	for _, existing := range queue {
		if !samePendingToolUseConfirm(existing, d) {
			filtered = append(filtered, existing)
		}
	}
	if len(filtered) == 0 {
		delete(a.toolUseConfirms, key)
		return
	}
	a.toolUseConfirms[key] = filtered
}

func samePendingToolUseConfirm(a, b *agentbuiltin.PendingToolUseConfirm) bool {
	if a == nil || b == nil {
		return a == b
	}
	if strings.TrimSpace(a.ToolUseID) != "" && strings.TrimSpace(b.ToolUseID) != "" {
		return a.ToolUseID == b.ToolUseID
	}
	return a == b
}

func assistantResultAfterRun(result *schema.Message, streamed string, ask *agentbuiltin.AskData, confirm *agentbuiltin.PendingToolUseConfirm) *schema.Message {
	if ask != nil {
		return schema.AssistantMessage(askPendingContent(ask), nil)
	}
	if streamed != "" {
		return schema.AssistantMessage(streamed, nil)
	}
	if result != nil && result.Content != "" {
		return result
	}
	if confirm != nil {
		return schema.AssistantMessage(toolUseConfirmPendingContent(confirm), nil)
	}
	return schema.AssistantMessage("命令或工具已执行完成，但模型没有生成后续回复。你可以继续输入下一步，或查看上面的工具结果。", nil)
}

func askPendingContent(ask *agentbuiltin.AskData) string {
	if ask == nil {
		return ""
	}
	question := strings.TrimSpace(ask.Question)
	if question == "" {
		return "等待你的选择。"
	}
	return "等待你的选择：" + question
}

func toolUseConfirmPendingContent(confirm *agentbuiltin.PendingToolUseConfirm) string {
	if confirm == nil {
		return ""
	}
	if strings.EqualFold(confirm.ToolName, "bash") {
		return bashApprovalPendingContent(confirm)
	}
	question := strings.TrimSpace(confirm.Question)
	if question == "" {
		question = "等待你确认工具调用。"
	}
	var b strings.Builder
	b.WriteString(question)
	if strings.TrimSpace(confirm.ToolUseID) != "" {
		b.WriteString("\n\ntool_use_id: ")
		b.WriteString(confirm.ToolUseID)
	}
	return b.String()
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
	SessionID     string
	Stream        func(delta string) bool
	SendTarget    agentchannel.SendTarget
	DisabledTools []string
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
		if opts.SessionID != "" {
			if _, err := a.sessionMgr.Get(ctx, opts.SessionID); err != nil {
				logger.Infof("session lookup failed: %v, fallback to conversationID", err)
			} else {
				memoryID = opts.SessionID
				usingSession = true
			}
		} else {
			sid, isNew, err := a.sessionMgr.GetOrCreate(ctx, channelName, channelUser, a.CurrentLLMModel())
			if err != nil {
				logger.Infof("session get/create failed: %v, fallback to conversationID", err)
			} else {
				memoryID = sid
				isNewSession = isNew
				usingSession = true
			}
		}
		if isNewSession {
			a.sessionMgr.SetTitle(ctx, memoryID, userText)
		}
	}

	logger.Infof("Agent.Chat called: conversation=%s device=%s memory=%s text=%q", conversationID, deviceID, memoryID, userText)
	ctx = withRunEventSession(ctx, memoryID)
	_ = publishRunEvent(ctx, a.eventBus, agentevent.TypeAgentRunStarted, memoryID, map[string]any{
		"conversation_id": conversationID,
		"device_id":       deviceID,
		"channel":         channelName,
		"model":           a.CurrentLLMModel(),
		"text":            userText,
	})

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
	var toolUseConfirmHolder *agentbuiltin.ToolUseConfirmHolder
	ctx, toolUseConfirmHolder = agentbuiltin.NewToolUseConfirmHolder(ctx)
	var commitRequestHolder *agentbuiltin.CommitRequestHolder
	ctx, commitRequestHolder = agentbuiltin.NewCommitRequestHolder(ctx)
	var sendStatus *agentbuiltin.ChannelSendStatus
	ctx, sendStatus = agentbuiltin.NewChannelSendStatus(ctx)
	ctx, toolRuns := withToolRunTracker(ctx)
	ctx = context.WithValue(ctx, agentbuiltin.SubAgentParentKey, memoryID)
	ctx = context.WithValue(ctx, agentbuiltin.SubAgentDeviceIDKey, deviceID)
	ctx = context.WithValue(ctx, agentbuiltin.SubAgentChannelKey, channelName)
	ctx = agentbuiltin.WithRecentImageConversation(ctx, memoryID)
	if opts.SendTarget.Channel != "" {
		ctx = agentchannel.WithSendTarget(ctx, opts.SendTarget)
	}

	einoTools := filterDisabledTools(ctx, a.toolsForChat(ctx, memoryID, deviceID, channelName), opts.DisabledTools)

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
		maxIterations = defaultAgentMaxIterations
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
		Agent:           agent,
		EnableStreaming: opts.Stream != nil,
	})

	runCtx := a.recorder.WithContext(ctx, modelID)
	events, err := runWithRetry(runCtx, runner, msgs)
	if err != nil {
		if sendStatus.Sent() {
			logger.Infof("Agent.Chat completed after channel_send despite runner error: %v", err)
			return a.completeAfterChannelSend(ctx, conversationID, memoryID, usingSession, channelName, channelUser, epochToday, history, userText, askHolder, toolUseConfirmHolder), nil
		}
		_ = publishRunEvent(ctx, a.eventBus, agentevent.TypeAgentRunFailed, memoryID, map[string]any{"error": err.Error()})
		return "", fmt.Errorf("agent error: %w", err)
	}

	var result *schema.Message
	var streamed strings.Builder
	for _, event := range events {
		if event.Err != nil {
			logger.Infof("Agent.Chat event error: %v", event.Err)
			if sendStatus.Sent() {
				logger.Infof("Agent.Chat completed after channel_send despite event error: %v", event.Err)
				return a.completeAfterChannelSend(ctx, conversationID, memoryID, usingSession, channelName, channelUser, epochToday, history, userText, askHolder, toolUseConfirmHolder), nil
			}
			_ = publishRunEvent(ctx, a.eventBus, agentevent.TypeAgentRunFailed, memoryID, map[string]any{"error": event.Err.Error()})
			return "", fmt.Errorf("agent error: %w", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		mv := event.Output.MessageOutput
		if mv.Role != schema.Assistant {
			continue
		}
		if mv.IsStreaming && mv.MessageStream != nil {
			for {
				chunk, recvErr := mv.MessageStream.Recv()
				if recvErr != nil {
					if recvErr == io.EOF {
						break
					}
					_ = publishRunEvent(ctx, a.eventBus, agentevent.TypeAgentRunFailed, memoryID, map[string]any{"error": recvErr.Error()})
					return "", fmt.Errorf("agent stream error: %w", recvErr)
				}
				if chunk != nil && chunk.Content != "" {
					streamed.WriteString(chunk.Content)
					if opts.Stream != nil && !opts.Stream(chunk.Content) {
						return "", ctx.Err()
					}
				}
			}
			continue
		}
		if mv.Message != nil {
			result = mv.Message
		}
	}
	ask := askHolder.Get()
	confirm := toolUseConfirmHolder.Get()
	confirms := toolUseConfirmHolder.All()
	resultLen := 0
	if result != nil {
		resultLen = len([]rune(result.Content))
	}
	logger.Infof("Agent.Chat post-run pending: ask=%v confirms=%d conversation=%s memory=%s streamed_len=%d result_len=%d", ask != nil, len(confirms), conversationID, memoryID, streamed.Len(), resultLen)
	result = assistantResultAfterRun(result, streamed.String(), ask, confirm)

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

	if ask != nil {
		a.storeAskData(conversationID, ask)
	}
	if request := commitRequestHolder.Get(); request != nil {
		a.storeCommitRequest(conversationID, request)
	}
	for _, confirm := range confirms {
		a.storeToolUseConfirm(ctx, conversationID, confirm)
	}
	if shouldContinueExecutionChecklist(ctx, history, userText, toolRuns.Count(), ask, confirms, sendStatus) {
		logger.Infof("execution checklist continuing: memory=%s tools=%d pass=%d", memoryID, toolRuns.Count(), executionChecklistContinuationCount(ctx)+1)
		return a.ChatWithContextOptions(
			context.WithValue(ctx, executionChecklistContinuationKey{}, executionChecklistContinuationCount(ctx)+1),
			conversationID,
			deviceID,
			"系统续办：用户的编号执行清单尚未确认全部完成。你刚完成了一项工具操作；除非明确受阻或需要用户确认，请立即继续执行下一项。不要总结或结束。",
			opts,
		)
	}

	_ = publishRunEvent(ctx, a.eventBus, agentevent.TypeAgentRunCompleted, memoryID, map[string]any{
		"message_len": len(result.Content),
		"history_len": len(updated),
	})
	return result.Content, nil
}

func shouldContinueExecutionChecklist(ctx context.Context, history []*schema.Message, userText string, toolRuns int, ask *agentbuiltin.AskData, confirms []*agentbuiltin.PendingToolUseConfirm, sendStatus *agentbuiltin.ChannelSendStatus) bool {
	return toolRuns > 0 &&
		ask == nil &&
		len(confirms) == 0 &&
		(sendStatus == nil || !sendStatus.Sent()) &&
		executionChecklistContinuationCount(ctx) < maxExecutionChecklistContinuations &&
		hasExecutionChecklist(history, userText)
}

func executionChecklistContinuationCount(ctx context.Context) int {
	count, _ := ctx.Value(executionChecklistContinuationKey{}).(int)
	return count
}

func hasExecutionChecklist(history []*schema.Message, userText string) bool {
	if isExecutionChecklistText(userText) {
		return true
	}
	for _, message := range history {
		if message != nil && message.Role == schema.User && isExecutionChecklistText(message.Content) {
			return true
		}
	}
	return false
}

func isExecutionChecklistText(text string) bool {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "执行清单") {
		return true
	}
	items := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= 3 && line[0] >= '1' && line[0] <= '9' && (strings.HasPrefix(line[1:], ".") || strings.HasPrefix(line[1:], "、") || strings.HasPrefix(line[1:], "）") || strings.HasPrefix(line[1:], ")")) {
			items++
		}
	}
	return items >= 2
}

func (a *Agent) completeAfterChannelSend(ctx context.Context, conversationID string, memoryID string, usingSession bool, channelName string, channelUser string, epochToday string, history []*schema.Message, userText string, askHolder *agentbuiltin.AskDataHolder, toolUseConfirmHolder *agentbuiltin.ToolUseConfirmHolder) string {
	result := schema.AssistantMessage("已发送。", nil)
	updated := append(history,
		schema.UserMessage(userText),
		result,
	)
	if err := a.memory.Save(ctx, memoryID, updated); err != nil {
		logger.Infof("memory save failed after channel_send completion: %v", err)
	} else if epochToday != "" {
		a.sessionMgr.SetEpoch(ctx, channelName, channelUser, epochToday)
	}
	if usingSession {
		a.sessionMgr.UpdateAfterChat(ctx, memoryID, len(updated))
	}
	if ask := askHolder.Get(); ask != nil {
		a.storeAskData(conversationID, ask)
	}
	for _, confirm := range toolUseConfirmHolder.All() {
		a.storeToolUseConfirm(ctx, conversationID, confirm)
	}
	_ = publishRunEvent(ctx, a.eventBus, agentevent.TypeAgentRunCompleted, memoryID, map[string]any{
		"message_len": len(result.Content),
		"history_len": len(updated),
		"after_send":  true,
	})
	return result.Content
}

func (a *Agent) toolGuide(interactive bool) string {
	current := a.CurrentLLMModel()
	var b strings.Builder
	b.WriteString(`=== 工具使用指引 ===
根据任务场景选择最合适的工具：

完成操作后的回复规则：
• 用户同意“后续操作静默”时，仅压缩执行过程中的逐步播报；任务结束后仍必须给出可核验的简短摘要，说明做了什么、每项结果以及关键产物或失败原因。
• 只有用户明确要求“完全静默”或“不需要任何结果”时，才可以省略最终摘要。

	• webfetch — 获取网页内容。用于读取 URL、查看网页、获取公开信息。支持 markdown/text/html 格式。不支持需要登录的页面。
	• websearch — 搜索网络。用于查询实时信息、最新新闻、事件和知识。`)
	if a.cfg.BashConfig.Enabled {
		b.WriteString(`
	• bash — 执行 shell 命令。用于系统诊断、查看状态、运行脚本。bash 默认在当前工作目录执行；不要使用 cd 切换目录，访问子目录请使用相对路径。需要执行命令时必须调用 bash 工具，不能只描述命令或让用户复制执行。所有命令均需用户确认；确认 UI 只能由 bash 工具触发，禁止手写“等待你确认执行 bash 命令”、tool_use_id、审批面板或类似确认块。`)
	}
	if interactive {
		b.WriteString(`
• ask_user_question — 向用户提问。需要用户选择、确认或补充信息时使用。问题以按钮（飞书）或文字列表展示。不会阻塞等待用户回答。
• memory_save — 记住用户信息。仅在用户明确说"记一下""记住""以后按这个来"时调用。
• memory_forget — 删除一条用户记忆。
• memory_list — 列出所有用户记忆。
• task — 子代理任务。将复杂、多步骤或独立的工作委托给子代理执行。子代理当前使用的模型是 ` + current + `，有独立的会话和工具集。
• device tools — 设备端工具（如拍照），用于需要摄像头或硬件交互的场景。仅在 ESP32 设备可用。`)
		if a.channelSendTool != nil {
			b.WriteString(`
• file_write — 将文本内容写成当前会话可用的本地文件。生成 Markdown/HTML/JSON 等中间文件时先用它获得 file_path，再交给 skill 或 channel_send。
• channel_send — 向当前对话渠道发送文本或本地文件。工具或 skill 生成用户需要的 PDF、图片、文件后，用 target=current 和 file_path 发给用户；中间产物、日志或敏感文件不要自动发送。`)
		}
		if a.visionAnalyzer != nil && a.recentImages != nil {
			b.WriteString(`
• inspect_recent_image — 查看当前会话最近发送的一张图片。用户追问刚才那张图、识别图片文字、询问图中细节时使用。`)
		}
	}
	b.WriteString(`
• MCP tools — 外部 MCP 协议工具，按需调用。`)
	return b.String()
}

func bashApprovalPendingContent(confirm *agentbuiltin.PendingToolUseConfirm) string {
	command := strings.TrimSpace(confirm.BashCommand)
	if command == "" {
		command = strings.TrimSpace(strings.TrimPrefix(confirm.Question, "是否允许执行命令："))
	}
	var b strings.Builder
	b.WriteString("等待你确认执行 bash 命令")
	if strings.TrimSpace(confirm.ToolUseID) != "" {
		b.WriteString("\n\ntool_use_id: ")
		b.WriteString(strings.TrimSpace(confirm.ToolUseID))
	}
	if command != "" {
		b.WriteString("\n\n```bash\n")
		b.WriteString(command)
		b.WriteString("\n```")
	}
	b.WriteString("\n\n请在下方审批面板选择允许或拒绝。")
	return b.String()
}

func (a *Agent) buildEnvContext(channelName, deviceID string) string {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	var b strings.Builder
	b.WriteString("=== 环境信息 ===")
	fmt.Fprintf(&b, "\n当前时间：%s %s %02d:%02d（%s）", now.Format("2006-01-02"), chineseWeekday[now.Weekday()], now.Hour(), now.Minute(), loc.String())
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
	return agentbuiltin.LoadMemories(ctx, a.memory.MemoryBackends(channelName, deviceID))
}

func (a *Agent) CurrentContext(ctx context.Context, channelName, deviceID string) *ContextUsage {
	if a.memory == nil {
		return nil
	}
	modelID := a.CurrentLLMModel()
	cfg := a.cfg.selectedLLMModelConfigFor(modelID)

	memoryID := deviceID
	if a.sessionMgr != nil && channelName != "" && deviceID != "" {
		if sid := a.sessionMgr.GetChannelSession(ctx, channelName, deviceID); sid != "" {
			memoryID = sid
		}
	}

	history := a.memory.Load(ctx, memoryID)
	systemMsgs := make([]string, 0, 4)
	if a.cfg.LLMPrompt != "" {
		systemMsgs = append(systemMsgs, a.cfg.LLMPrompt)
	}
	systemMsgs = append(systemMsgs, a.buildEnvContext(channelName, deviceID))
	systemMsgs = append(systemMsgs, a.toolGuide(true))
	if memories := a.loadMemories(ctx, channelName, deviceID); memories != "" {
		systemMsgs = append(systemMsgs, memories)
	}

	var estimated int
	for _, s := range systemMsgs {
		estimated += len(s) / 4
	}
	for _, m := range history {
		estimated += len(m.Content) / 4
	}

	reserve := 8000
	compressAt := cfg.ContextLength*75/100 - cfg.MaxTokens - reserve
	if compressAt < 2000 {
		compressAt = 2000
	}

	return &ContextUsage{
		Model:          modelID,
		ContextLength:  cfg.ContextLength,
		MaxTokens:      cfg.MaxTokens,
		EstimatedInput: estimated,
		CompressAt:     compressAt,
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
		MaxIterations:    defaultAgentMaxIterations,
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

func (a *Agent) RunPromptProfile(ctx context.Context, req PromptProfileRequest) (string, error) {
	if strings.TrimSpace(req.SystemPrompt) == "" {
		return "", fmt.Errorf("profile system prompt is required")
	}
	userText := strings.TrimSpace(req.UserText)
	if userText == "" {
		return "", fmt.Errorf("profile user text is required")
	}
	profileSystemAsUser := req.ChannelName == "a2a" && req.AllowTools && a.a2aSkillMW != nil
	msgs, profileSessionID := a.buildPromptProfileMessages(ctx, req.SystemPrompt, userText, req.ChannelName, req.SessionKey, req.Model, profileSystemAsUser, req.DisableHistory)
	historyMsgs := promptProfilePersistMessages(msgs, userText)

	chatModel, modelID, err := a.chatModelForID(ctx, req.Model)
	if err != nil {
		return "", fmt.Errorf("create chat model: %w", err)
	}
	maxSteps := promptProfileMaxSteps(req.MaxSteps)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "a2a_profile"
	}
	if req.ChannelName == "a2a" {
		ctx = withTrace(ctx, newA2ATraceState(name, req.SessionKey, newA2ATraceOptions(a.cfg)))
	}
	cfg := &adk.ChatModelAgentConfig{
		Name:             name,
		Model:            chatModel,
		MaxIterations:    maxSteps,
		ModelRetryConfig: newLLMRetryConfig(),
	}
	if req.ChannelName == "a2a" && a.a2aSkillMW != nil {
		cfg.Handlers = []adk.ChatModelAgentMiddleware{a.a2aSkillMW}
	} else if req.ChannelName != "a2a" && a.skillMW != nil {
		cfg.Handlers = []adk.ChatModelAgentMiddleware{a.skillMW}
	}
	var einoTools []tool.BaseTool
	if req.AllowTools {
		einoTools = a.subAgentTools(ctx, true, req.ChannelName)
	}
	if req.StructuredOutput != nil {
		einoTools = append(einoTools, newPromptProfileStructuredOutputTool(req.StructuredOutput))
	}
	if len(einoTools) > 0 {
		cfg.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: einoTools,
			},
		}
	}
	if st := traceFromContext(ctx); st != nil {
		msg := fmt.Sprintf("%s profile.start allow_tools=%v max_steps=%d input_len=%d tools=%v", tracePrefix(st), req.AllowTools, maxSteps, len(userText), traceToolNames(ctx, einoTools))
		if st.Options.LogInputs {
			msg += fmt.Sprintf(" input=%q", traceTruncate(userText, st.Options.MaxValueLength))
		}
		logger.Infof("%s", msg)
	}

	agent, err := adk.NewChatModelAgent(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("create profile agent: %w", err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	runCtx := a.recorder.WithContext(ctx, modelID)
	events, err := runWithRetry(runCtx, runner, msgs)
	if err != nil {
		errorMsg := fmt.Sprintf("[执行失败] %v", err)
		a.savePromptProfileDiagnostic(ctx, profileSessionID, historyMsgs, errorMsg)
		return "", fmt.Errorf("profile agent error: %w", err)
	}
	var result string
	for _, event := range events {
		if event.Err != nil {
			logger.Infof("RunPromptProfile event error: %v", event.Err)
			errorMsg := fmt.Sprintf("[执行失败] %v", event.Err)
			a.savePromptProfileDiagnostic(ctx, profileSessionID, historyMsgs, errorMsg)
			return "", fmt.Errorf("profile agent error: %w", event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil &&
			event.Output.MessageOutput.Message != nil &&
			event.Output.MessageOutput.Role == schema.Assistant {
			result = cleanPromptProfileResult(event.Output.MessageOutput.Message.Content)
		}
	}
	if result == "" {
		errorMsg := "[执行失败] 代理返回空响应"
		a.savePromptProfileDiagnostic(ctx, profileSessionID, historyMsgs, errorMsg)
		return "", fmt.Errorf("profile agent returned empty response")
	}
	a.savePromptProfileHistory(ctx, profileSessionID, historyMsgs, result)
	return result, nil
}

func (a *Agent) RunPromptProfileStream(ctx context.Context, req PromptProfileRequest, emit func(PromptProfileStreamEvent) bool) (PromptProfileStreamReply, error) {
	if strings.TrimSpace(req.SystemPrompt) == "" {
		return PromptProfileStreamReply{}, fmt.Errorf("profile system prompt is required")
	}
	userText := strings.TrimSpace(req.UserText)
	if userText == "" {
		return PromptProfileStreamReply{}, fmt.Errorf("profile user text is required")
	}
	if emit == nil {
		emit = func(PromptProfileStreamEvent) bool { return true }
	}
	profileSystemAsUser := req.ChannelName == "a2a" && req.AllowTools && a.a2aSkillMW != nil
	msgs, profileSessionID := a.buildPromptProfileMessages(ctx, req.SystemPrompt, userText, req.ChannelName, req.SessionKey, req.Model, profileSystemAsUser, req.DisableHistory)
	historyMsgs := promptProfilePersistMessages(msgs, userText)

	chatModel, modelID, err := a.chatModelForID(ctx, req.Model)
	if err != nil {
		return PromptProfileStreamReply{}, fmt.Errorf("create chat model: %w", err)
	}
	maxSteps := promptProfileMaxSteps(req.MaxSteps)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "a2a_profile"
	}
	if req.ChannelName == "a2a" {
		ctx = withTrace(ctx, newA2ATraceState(name, req.SessionKey, newA2ATraceOptions(a.cfg)))
	}
	cfg := &adk.ChatModelAgentConfig{
		Name:             name,
		Model:            chatModel,
		MaxIterations:    maxSteps,
		ModelRetryConfig: newLLMRetryConfig(),
	}
	if req.ChannelName == "a2a" && a.a2aSkillMW != nil {
		cfg.Handlers = []adk.ChatModelAgentMiddleware{a.a2aSkillMW}
	} else if req.ChannelName != "a2a" && a.skillMW != nil {
		cfg.Handlers = []adk.ChatModelAgentMiddleware{a.skillMW}
	}
	var einoTools []tool.BaseTool
	if req.AllowTools {
		einoTools = a.subAgentTools(ctx, true, req.ChannelName)
		if len(einoTools) > 0 {
			cfg.ToolsConfig = adk.ToolsConfig{
				ToolsNodeConfig: compose.ToolsNodeConfig{
					Tools: einoTools,
				},
			}
		}
	}
	if st := traceFromContext(ctx); st != nil {
		msg := fmt.Sprintf("%s profile.stream.start allow_tools=%v max_steps=%d input_len=%d tools=%v", tracePrefix(st), req.AllowTools, maxSteps, len(userText), traceToolNames(ctx, einoTools))
		if st.Options.LogInputs {
			msg += fmt.Sprintf(" input=%q", traceTruncate(userText, st.Options.MaxValueLength))
		}
		logger.Infof("%s", msg)
	}

	agent, err := adk.NewChatModelAgent(ctx, cfg)
	if err != nil {
		return PromptProfileStreamReply{}, fmt.Errorf("create profile agent: %w", err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	runCtx := a.recorder.WithContext(ctx, modelID)
	it := runner.Run(runCtx, msgs)

	var result strings.Builder
	eventIndex := 0
	for {
		event, ok := it.Next()
		if !ok {
			break
		}
		eventIndex++
		logTraceEvent(runCtx, eventIndex, event)
		if event.Err != nil {
			logger.Infof("RunPromptProfileStream event error: %v", event.Err)
			errorMsg := fmt.Sprintf("[执行失败] %v", event.Err)
			a.savePromptProfileDiagnostic(ctx, profileSessionID, historyMsgs, errorMsg)
			return PromptProfileStreamReply{}, fmt.Errorf("profile agent error: %w", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		mv := event.Output.MessageOutput
		if mv.Role == schema.Tool {
			toolName := strings.TrimSpace(mv.ToolName)
			if toolName == "" && mv.Message != nil {
				toolName = strings.TrimSpace(mv.Message.Name)
			}
			if toolName == "" {
				toolName = "tool"
			}
			continue
		}
		if mv.Role != schema.Assistant {
			continue
		}
		if mv.IsStreaming && mv.MessageStream != nil {
			for {
				chunk, recvErr := mv.MessageStream.Recv()
				if recvErr != nil {
					if recvErr == io.EOF {
						break
					}
					errorMsg := fmt.Sprintf("[执行失败] %v", recvErr)
					a.savePromptProfileDiagnostic(ctx, profileSessionID, historyMsgs, errorMsg)
					return PromptProfileStreamReply{}, fmt.Errorf("profile agent stream error: %w", recvErr)
				}
				if chunk != nil && chunk.Content != "" {
					result.WriteString(chunk.Content)
					emit(PromptProfileStreamEvent{Kind: PromptProfileStreamAnswerDelta, Delta: chunk.Content})
				}
			}
			continue
		}
		if mv.Message != nil {
			result.Reset()
			result.WriteString(mv.Message.Content)
		}
	}

	raw := cleanPromptProfileResult(result.String())
	if strings.TrimSpace(raw) == "" {
		errorMsg := "[执行失败] 代理返回空响应"
		a.savePromptProfileDiagnostic(ctx, profileSessionID, historyMsgs, errorMsg)
		return PromptProfileStreamReply{}, fmt.Errorf("profile agent returned empty response")
	}
	reply := parsePromptProfileStreamReply(raw)
	a.savePromptProfileHistory(ctx, profileSessionID, historyMsgs, raw)
	return reply, nil
}

func parsePromptProfileStreamReply(raw string) PromptProfileStreamReply {
	raw = strings.TrimSpace(raw)
	var structured struct {
		Reasoning string `json:"reasoning"`
		Thinking  string `json:"thinking"`
		Answer    string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(raw), &structured); err != nil {
		return PromptProfileStreamReply{Answer: raw}
	}
	reasoning := strings.TrimSpace(structured.Reasoning)
	if reasoning == "" {
		reasoning = strings.TrimSpace(structured.Thinking)
	}
	answer := strings.TrimSpace(structured.Answer)
	if answer == "" {
		answer = raw
	}
	return PromptProfileStreamReply{Answer: answer, Reasoning: reasoning}
}

func promptProfileMaxSteps(value int) int {
	if value > 0 {
		return value
	}
	return defaultAgentMaxIterations
}

func (a *Agent) buildPromptProfileMessages(ctx context.Context, systemPrompt, userText, channelName, sessionKey, model string, systemAsUser bool, disableHistory bool) ([]*schema.Message, string) {
	msgs := make([]*schema.Message, 0, 4)
	if !systemAsUser {
		msgs = append(msgs, schema.SystemMessage(systemPrompt))
	}

	sessionID := ""
	if !disableHistory && strings.TrimSpace(sessionKey) != "" && a != nil && a.sessionMgr != nil && a.memory != nil {
		modelID := strings.TrimSpace(model)
		if modelID == "" {
			modelID = strings.TrimSpace(a.CurrentLLMModel())
		}
		if modelID == "" {
			modelID = strings.TrimSpace(a.cfg.LLMModel)
		}
		if id, _, err := a.sessionMgr.GetOrCreate(ctx, promptProfileSessionChannel(channelName), sessionKey, modelID); err == nil {
			sessionID = id
			msgs = append(msgs, filterDiagnosticMessages(a.memory.Load(ctx, sessionID))...)
		} else {
			logger.Infof("profile session get/create failed: %v", err)
		}
	}

	if systemAsUser {
		msgs = append(msgs, schema.UserMessage(promptProfileUserContent(systemPrompt, userText)))
	} else {
		msgs = append(msgs, schema.UserMessage(userText))
	}
	return msgs, sessionID
}

func promptProfileUserContent(systemPrompt, userText string) string {
	return strings.TrimSpace(fmt.Sprintf("任务说明：\n%s\n\n当前输入：\n%s", strings.TrimSpace(systemPrompt), strings.TrimSpace(userText)))
}

func cleanPromptProfileResult(result string) string {
	result = strings.TrimSpace(result)
	if !strings.HasPrefix(result, "```") {
		return result
	}
	lineEnd := strings.IndexByte(result, '\n')
	if lineEnd < 0 {
		return result
	}
	body := strings.TrimSpace(result[lineEnd+1:])
	if strings.HasSuffix(body, "```") {
		body = strings.TrimSpace(strings.TrimSuffix(body, "```"))
	}
	return body
}

func promptProfilePersistMessages(msgs []*schema.Message, userText string) []*schema.Message {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]*schema.Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		cp := *msg
		out = append(out, &cp)
	}
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role == schema.User {
			out[i].Content = userText
			break
		}
	}
	return out
}

func promptProfileSessionChannel(channelName string) string {
	if channelName == "a2a" {
		return "a2a_profile"
	}
	return "profile"
}

func (a *Agent) savePromptProfileHistory(ctx context.Context, sessionID string, msgs []*schema.Message, result string) {
	a.savePromptProfileHistoryWithOptions(ctx, sessionID, msgs, result, false)
}

func (a *Agent) savePromptProfileDiagnostic(ctx context.Context, sessionID string, msgs []*schema.Message, result string) {
	a.savePromptProfileHistoryWithOptions(ctx, sessionID, msgs, result, true)
}

func (a *Agent) savePromptProfileHistoryWithOptions(ctx context.Context, sessionID string, msgs []*schema.Message, result string, diagnostic bool) {
	if strings.TrimSpace(sessionID) == "" || a == nil || a.memory == nil {
		return
	}
	updated := make([]*schema.Message, 0, len(msgs)+1)
	for _, msg := range msgs {
		if msg == nil || msg.Role == schema.System {
			continue
		}
		updated = append(updated, msg)
	}
	assistantMsg := &schema.Message{Role: schema.Assistant, Content: result}
	if diagnostic {
		assistantMsg.Extra = map[string]any{"diagnostic": true}
	}
	updated = append(updated, assistantMsg)
	if err := a.memory.Save(ctx, sessionID, updated); err != nil {
		logger.Infof("profile memory save failed: %v", err)
		return
	}
	if a.sessionMgr != nil {
		a.sessionMgr.UpdateAfterChat(ctx, sessionID, len(updated))
	}
}

func filterDiagnosticMessages(msgs []*schema.Message) []*schema.Message {
	if len(msgs) == 0 {
		return msgs
	}
	filtered := make([]*schema.Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil || isDiagnosticMessage(msg) {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func isDiagnosticMessage(msg *schema.Message) bool {
	if msg == nil || len(msg.Extra) == 0 {
		return false
	}
	value, ok := msg.Extra["diagnostic"]
	if !ok {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true") || v == "1"
	default:
		return false
	}
}

// RunNamedSubAgent invokes a registered subagent by name with the given
// prompt, bypassing the main agent. Used by A2A routing to direct public
// queries to the a2a_public_assistant subagent. sessionKey scopes the
// subagent's memory; channelName restricts its tools via toolsForChat.
func (a *Agent) RunNamedSubAgent(ctx context.Context, name string, prompt string, sessionKey string, channelName string) (string, error) {
	if a.taskTool == nil {
		return "", fmt.Errorf("subagent registry not available")
	}
	spec, ok := a.taskTool.SubAgentSpecByName(name)
	if !ok {
		return "", fmt.Errorf("unknown subagent %q", name)
	}
	rt := agentbuiltin.SubAgentRuntime{
		TaskID:      sessionKey,
		SessionKey:  sessionKey,
		ChannelName: channelName,
	}
	return a.SubAgent(ctx, spec, rt, prompt)
}

func (a *Agent) SubAgent(ctx context.Context, spec agentbuiltin.SubAgentSpec, rt agentbuiltin.SubAgentRuntime, prompt string) (string, error) {
	if spec.IsFork {
		return a.runForkSubAgent(ctx, spec, rt, prompt)
	}
	return a.runNormalSubAgent(ctx, spec, rt, prompt)
}

func (a *Agent) runNormalSubAgent(ctx context.Context, spec agentbuiltin.SubAgentSpec, rt agentbuiltin.SubAgentRuntime, prompt string) (string, error) {
	if rt.ChannelName == "a2a" {
		ctx = withTrace(ctx, newA2ATraceState(spec.Name, rt.SessionKey, newA2ATraceOptions(a.cfg)))
	}
	msgs := make([]*schema.Message, 0, 2)
	if spec.SystemPrompt != "" {
		msgs = append(msgs, schema.SystemMessage(spec.SystemPrompt))
	}
	sessionKey := subAgentHistorySessionKey(rt)
	// Disable memory entirely for A2A channel - no conversation history
	// persistence to avoid any cross-session leakage risks.
	if sessionKey != "" && rt.ChannelName != "a2a" && a.sessionMgr != nil && a.memory != nil {
		subSession, _, err := a.sessionMgr.GetOrCreate(ctx, "subagent", sessionKey, a.CurrentLLMModel())
		if err == nil {
			if history := filterDiagnosticMessages(a.memory.Load(ctx, subSession)); len(history) > 0 {
				msgs = append(msgs, history...)
			}
		}
	}
	msgs = append(msgs, schema.UserMessage(prompt))

	einoTools := a.subAgentTools(ctx, spec.AllowTools, rt.ChannelName)
	if len(spec.DisabledTools) > 0 {
		disabled := make(map[string]bool, len(spec.DisabledTools))
		for _, name := range spec.DisabledTools {
			disabled[name] = true
		}
		filtered := make([]tool.BaseTool, 0, len(einoTools))
		for _, t := range einoTools {
			info, err := t.Info(ctx)
			if err != nil || info == nil {
				continue
			}
			if disabled[info.Name] {
				continue
			}
			filtered = append(filtered, t)
		}
		einoTools = filtered
	}

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
	// Skill middleware 选择：A2A 用 skill 白名单版本，其他用完整版
	if rt.ChannelName == "a2a" && a.a2aSkillMW != nil {
		cfg.Handlers = []adk.ChatModelAgentMiddleware{a.a2aSkillMW}
	} else if rt.ChannelName != "a2a" && a.skillMW != nil {
		cfg.Handlers = []adk.ChatModelAgentMiddleware{a.skillMW}
	}
	if len(einoTools) > 0 {
		cfg.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: einoTools,
			},
		}
	}
	if st := traceFromContext(ctx); st != nil {
		msg := fmt.Sprintf("%s subagent.start allow_tools=%v max_steps=%d input_len=%d tools=%v", tracePrefix(st), spec.AllowTools, maxSteps, len(prompt), traceToolNames(ctx, einoTools))
		if st.Options.LogInputs {
			msg += fmt.Sprintf(" input=%q", traceTruncate(prompt, st.Options.MaxValueLength))
		}
		logger.Infof("%s", msg)
	}

	agent, err := adk.NewChatModelAgent(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("create subagent: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	runCtx := a.recorder.WithContext(ctx, modelID)

	events, err := runWithRetry(runCtx, runner, msgs)
	if err != nil {
		errorMsg := fmt.Sprintf("[执行失败] %v", err)
		a.saveSubAgentHistory(ctx, sessionKey, prompt, errorMsg, true)
		return "", fmt.Errorf("subagent error: %w", err)
	}

	var result string
	for _, event := range events {
		if event.Err != nil {
			logger.Infof("SubAgent event error: %v", event.Err)
			errorMsg := fmt.Sprintf("[执行失败] %v", event.Err)
			a.saveSubAgentHistory(ctx, sessionKey, prompt, errorMsg, true)
			return "", fmt.Errorf("subagent error: %w", event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil &&
			event.Output.MessageOutput.Message != nil &&
			event.Output.MessageOutput.Role == schema.Assistant {
			result = event.Output.MessageOutput.Message.Content
		}
	}
	if result == "" {
		errorMsg := "[执行失败] 代理返回空响应"
		a.saveSubAgentHistory(ctx, sessionKey, prompt, errorMsg, true)
		return "", fmt.Errorf("subagent returned empty response")
	}

	a.saveSubAgentHistory(ctx, sessionKey, prompt, result, false)
	return result, nil
}

func (a *Agent) saveSubAgentHistory(ctx context.Context, sessionKey string, prompt string, result string, diagnostic bool) {
	if sessionKey == "" || a.sessionMgr == nil || a.memory == nil {
		return
	}
	subSession, _, _ := a.sessionMgr.GetOrCreate(ctx, "subagent", sessionKey, a.CurrentLLMModel())
	if subSession == "" {
		return
	}
	updated := a.memory.Load(ctx, subSession)
	updated = append(updated, schema.UserMessage(prompt))
	assistantMsg := &schema.Message{Role: schema.Assistant, Content: result}
	if diagnostic {
		assistantMsg.Extra = map[string]any{"diagnostic": true}
	}
	updated = append(updated, assistantMsg)
	a.memory.Save(ctx, subSession, updated)
}

func subAgentHistorySessionKey(rt agentbuiltin.SubAgentRuntime) string {
	if rt.ChannelName == "a2a" {
		return ""
	}
	return rt.SessionKey
}

func (a *Agent) runForkSubAgent(ctx context.Context, spec agentbuiltin.SubAgentSpec, rt agentbuiltin.SubAgentRuntime, prompt string) (string, error) {
	var parentHistory []*schema.Message
	if rt.ParentSession != "" && a.memory != nil {
		parentHistory = a.memory.Load(ctx, rt.ParentSession)
	}

	msgs := make([]*schema.Message, 0, len(parentHistory)+6)
	if a.cfg.LLMPrompt != "" {
		msgs = append(msgs, schema.SystemMessage(a.cfg.LLMPrompt))
	}
	msgs = append(msgs, schema.SystemMessage(a.buildEnvContext(rt.ChannelName, rt.DeviceID)))
	msgs = append(msgs, schema.SystemMessage(a.toolGuide(true)))
	if spec.SystemPrompt != "" {
		msgs = append(msgs, schema.SystemMessage(spec.SystemPrompt))
	}
	if memories := a.loadMemories(ctx, rt.ChannelName, rt.DeviceID); memories != "" {
		msgs = append(msgs, schema.SystemMessage(memories))
	}
	msgs = append(msgs, parentHistory...)
	msgs = append(msgs, schema.UserMessage(prompt))

	einoTools := a.forkTools(ctx, spec)

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
	// Skill middleware 选择：A2A 用 skill 白名单版本，其他用完整版
	if rt.ChannelName == "a2a" && a.a2aSkillMW != nil {
		cfg.Handlers = []adk.ChatModelAgentMiddleware{a.a2aSkillMW}
	} else if rt.ChannelName != "a2a" && a.skillMW != nil {
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

	sessionKey := rt.SessionKey
	if sessionKey == "" {
		sessionKey = rt.TaskID
	}
	if sessionKey != "" && a.sessionMgr != nil && a.memory != nil {
		subSession, _, _ := a.sessionMgr.GetOrCreate(ctx, "subagent", sessionKey, a.CurrentLLMModel())
		if subSession != "" {
			updated := a.memory.Load(ctx, subSession)
			updated = append(updated, schema.UserMessage(prompt))
			updated = append(updated, &schema.Message{Role: schema.Assistant, Content: result})
			a.memory.Save(ctx, subSession, updated)
		}
	}

	return result, nil
}

func (a *Agent) forkTools(ctx context.Context, spec agentbuiltin.SubAgentSpec) []tool.BaseTool {
	if !spec.AllowTools {
		return nil
	}
	parentTools := a.subAgentTools(ctx, true, "")
	// Block tools unsafe for background fork execution, plus any the agent disabled.
	blocked := map[string]bool{
		"task": true, "ask_user_question": true,
		"memory_save": true, "memory_forget": true,
		"bash": true,
	}
	for _, name := range spec.DisabledTools {
		blocked[name] = true
	}
	filtered := make([]tool.BaseTool, 0, len(parentTools))
	for _, t := range parentTools {
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		if blocked[info.Name] {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

func (a *Agent) subAgentTools(ctx context.Context, allowTools bool, channelName string) []tool.BaseTool {
	if !allowTools {
		return nil
	}
	if channelName == "a2a" {
		return a.a2aPublicTools(ctx)
	}

	filter := agentbuiltin.ToolWebSearch
	if a.cfg.BuiltinWebFetchEnabled {
		filter |= agentbuiltin.ToolWebFetch
	}
	einoTools := a.wrapBuiltinTools(agentbuiltin.NewFilteredTools(filter, agentbuiltin.ToolOptions{}))
	for _, tools := range a.extToolSets {
		for _, t := range tools {
			einoTools = append(einoTools, a.WrapTool(t, "mcp"))
		}
	}
	return einoTools
}

func (a *Agent) generateTools(ctx context.Context) []tool.BaseTool {
	filter := agentbuiltin.ToolWebSearch
	if a.cfg.BuiltinWebFetchEnabled {
		filter |= agentbuiltin.ToolWebFetch
	}
	einoTools := a.wrapBuiltinTools(agentbuiltin.NewFilteredTools(filter, agentbuiltin.ToolOptions{}))
	for _, tools := range a.extToolSets {
		for _, t := range tools {
			einoTools = append(einoTools, a.WrapTool(t, "mcp"))
		}
	}
	return einoTools
}

func (a *Agent) toolsForChat(_ context.Context, memoryID string, deviceID string, channelName string) []tool.BaseTool {
	// A2A channel: strictly limited to websearch + webfetch + CYEAM MCP.
	// No bash, reminders, logs, ask_user_question, memory tools, task tool,
	// channel_send, or device tools. Keep this path locked down even though
	// the A2A server currently routes through RunNamedSubAgent.
	if channelName == "a2a" {
		return a.a2aPublicTools(context.Background())
	}

	filter := agentbuiltin.ToolWebSearch | agentbuiltin.ToolAskUserQuestion
	if channelName == "tui" {
		filter |= agentbuiltin.ToolCommit
	}
	if a.cfg.BuiltinWebFetchEnabled {
		filter |= agentbuiltin.ToolWebFetch
	}
	if a.visionAnalyzer != nil && a.recentImages != nil && channelName != "a2a" {
		filter |= agentbuiltin.ToolInspectRecentImage
	}

	if a.cfg.BashConfig.Enabled && bashAllowedForChannel(channelName) {
		filter |= agentbuiltin.ToolBash
	}
	opts := agentbuiltin.ToolOptions{}
	if a.cfg.BashConfig.Enabled && bashAllowedForChannel(channelName) {
		opts.ShellConfig = &agentbuiltin.ShellConfig{
			Timeout:        a.cfg.BashConfig.Timeout,
			MaxOutputBytes: a.cfg.BashConfig.MaxOutputBytes,
			PolicyPath:     a.cfg.BashConfig.PolicyPath,
		}
	}
	if a.memory != nil && channelName != "" && deviceID != "" {
		filter |= agentbuiltin.ToolMemorySave | agentbuiltin.ToolMemoryForget | agentbuiltin.ToolMemoryList
		opts.MemoryBackends = a.memory.MemoryBackends(channelName, deviceID)
	}
	if a.cfg.ReminderStore != nil {
		filter |= agentbuiltin.ToolReminder
		opts.ReminderStore = a.cfg.ReminderStore
		opts.Timezone = a.cfg.Timezone
	}
	if a.cfg.LogDir != "" {
		filter |= agentbuiltin.ToolLog
		opts.LogDir = a.cfg.LogDir
	}
	if filter&agentbuiltin.ToolInspectRecentImage != 0 {
		opts.VisionAnalyzer = a.visionAnalyzer
		opts.RecentImages = a.recentImages
	}
	if len(a.fileWriteRoots) > 0 {
		filter |= agentbuiltin.ToolFileWrite
		opts.FileWriteRoots = a.fileWriteRoots
	}
	if len(a.agentFileRoots) > 0 {
		filter |= agentbuiltin.ToolCodeFiles
		opts.FileRoots = a.agentFileRoots
	}
	einoTools := a.wrapBuiltinTools(agentbuiltin.NewFilteredTools(filter, opts))
	if a.taskTool != nil {
		einoTools = append(einoTools, a.WrapTool(a.taskTool, "builtin"))
	}
	if a.channelSendTool != nil {
		einoTools = append(einoTools, a.WrapTool(a.channelSendTool, "builtin"))
	}
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

func bashAllowedForChannel(channelName string) bool {
	return channelName == string(agentchannel.TypeLark) || channelName == "tui"
}

func (a *Agent) a2aPublicTools(ctx context.Context) []tool.BaseTool {
	filter := agentbuiltin.ToolWebSearch
	if a.cfg.BuiltinWebFetchEnabled {
		filter |= agentbuiltin.ToolWebFetch
	}
	einoTools := a.wrapBuiltinTools(agentbuiltin.NewFilteredTools(filter, agentbuiltin.ToolOptions{}))
	for serverIdx, tools := range a.extToolSets {
		serverName := "unknown"
		if serverIdx < len(a.extMCPNames) {
			serverName = a.extMCPNames[serverIdx]
		}
		if !A2AAllowedMCPServers[serverName] {
			continue
		}
		for _, t := range tools {
			einoTools = append(einoTools, a.WrapTool(t, "mcp"))
		}
	}
	return einoTools
}

func (a *Agent) wrapBuiltinTools(tools []tool.BaseTool) []tool.BaseTool {
	wrapped := make([]tool.BaseTool, 0, len(tools))
	for _, t := range tools {
		wrapped = append(wrapped, a.WrapTool(t, "builtin"))
	}
	return wrapped
}

func filterDisabledTools(ctx context.Context, tools []tool.BaseTool, disabled []string) []tool.BaseTool {
	if len(tools) == 0 || len(disabled) == 0 {
		return tools
	}
	blocked := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		name = strings.TrimSpace(name)
		if name != "" {
			blocked[name] = true
		}
	}
	if len(blocked) == 0 {
		return tools
	}
	filtered := make([]tool.BaseTool, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil || info == nil || blocked[info.Name] {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}
