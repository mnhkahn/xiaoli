package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"github.com/mnhkahn/gogogo/logger"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"
	agentchannel "xiaoli/server/internal/agent/channel"
	agentlark "xiaoli/server/internal/agent/channel/lark"
	agentwechat "xiaoli/server/internal/agent/channel/wechat"
	agentmedia "xiaoli/server/internal/agent/media"
	agentruntime "xiaoli/server/internal/agent/runtime"
	agentworkflow "xiaoli/server/internal/agent/workflow"
	agentesp32 "xiaoli/server/internal/esp32"
	esp32audio "xiaoli/server/internal/esp32/audio"
)

type SpeechRecognizer = agentmedia.SpeechRecognizer

type VisionAnalyzer = agentmedia.VisionAnalyzer

type EinoAgent = agentruntime.Agent

type ChatOptions = agentruntime.ChatOptions

type memoryReader = agentruntime.MemoryReader

type memoryKeyInfo = agentruntime.MemoryKeyInfo

type memoryValue = agentruntime.MemoryValue

func newOpenAITranscriber(cfg Config) SpeechRecognizer {
	return agentmedia.NewOpenAITranscriber(agentmedia.ASRConfig{
		URL:     cfg.GoASRURL,
		APIKey:  cfg.GoASRAPIKey,
		Model:   cfg.GoASRModel,
		Timeout: cfg.GoASRTimeout,
	})
}

func newGoVisionClient(cfg Config) VisionAnalyzer {
	return agentmedia.NewOpenAIVisionClient(agentmedia.VisionConfig{
		URL:     cfg.GoVLLMURL,
		APIKey:  cfg.GoVLLMAPIKey,
		Model:   cfg.GoVLLMModel,
		Timeout: cfg.GoVLLMTimeout,
	})
}

func newEinoAgent(cfg Config) *EinoAgent {
	return agentruntime.NewAgent(agentruntime.Config{
		LLMURL:                  cfg.GoLLMURL,
		LLMAPIKey:               cfg.GoLLMAPIKey,
		LLMModel:                cfg.GoLLMModel,
		LLMModels:               cfg.GoLLMModels,
		LLMModelConfigs:         runtimeLLMModelConfigs(cfg.GoLLMModelConfigs),
		LLMPrompt:               cfg.GoLLMPrompt,
		LLMTimeout:              cfg.GoLLMTimeout,
		VLLMModel:               cfg.GoVLLMModel,
		ASRModel:                cfg.GoASRModel,
		TTSModel:                cfg.GoTTSModel,
		RedisURL:                cfg.RedisURL,
		RedisKeyPrefix:          cfg.RedisKeyPrefix,
		MemoryTTL:               cfg.MemoryTTL,
		ExternalMCPURLs:         cfg.ExternalMCPURLs,
		BuiltinWebFetchEnabled:  cfg.BuiltinWebFetchEnabled,
		SkillRoots:              cfg.SkillRoots,
		EnabledSkills:           cfg.EnabledSkills,
		SkillMaxBytes:           cfg.SkillMaxBytes,
		SkillExecTimeout:        cfg.SkillExecTimeout,
		SkillExecMaxOutputBytes: cfg.SkillExecMaxOutputBytes,
		SkillExecGlobalBinDirs:  cfg.SkillExecGlobalBinDirs,
	})
}

func runtimeLLMModelConfigs(models map[string]LLMModelConfig) map[string]agentruntime.LLMModelConfig {
	if len(models) == 0 {
		return nil
	}
	out := make(map[string]agentruntime.LLMModelConfig, len(models))
	for id, model := range models {
		out[id] = agentruntime.LLMModelConfig{
			ID:          model.ID,
			DisplayName: model.DisplayName,
			BaseURL:     model.BaseURL,
			Model:       model.Model,
			APIKey:      model.APIKey,
		}
	}
	return out
}

func newRedisMemory(cfg Config) memoryReader {
	return agentruntime.NewRedisMemory(agentruntime.Config{
		RedisURL:       cfg.RedisURL,
		RedisKeyPrefix: cfg.RedisKeyPrefix,
		MemoryTTL:      cfg.MemoryTTL,
	})
}

type DeviceController interface {
	Devices(ctx context.Context) ([]Device, error)
	Tools(ctx context.Context, deviceID string) (ToolListResponse, error)
	Call(ctx context.Context, request BridgeCallRequest) (BridgeCallResult, error)
	Speak(ctx context.Context, deviceID string, text string) (map[string]any, error)
	StopSpeak(ctx context.Context, deviceID string) (map[string]any, error)
}

type DeviceHub struct {
	*agentesp32.Hub
	conversation *deviceConversationAdapter
	vision       VisionAnalyzer
	tts          SpeechSynthesizer
}

func NewDeviceHub(cfg Config, stream *streamHub, audio *audioStore, asr SpeechRecognizer, agent *EinoAgent, vision VisionAnalyzer, tts SpeechSynthesizer) *DeviceHub {
	conversation := &deviceConversationAdapter{}
	hub := agentesp32.NewHub(agentesp32.HubConfig{
		PublicBaseURL:     cfg.PublicBaseURL,
		DeviceAuthKey:     cfg.DeviceAuthKey,
		AllowedDeviceIDs:  cfg.AllowedDeviceIDs,
		DeviceAuthEnabled: cfg.DeviceAuthEnabled,
	}, agentesp32.Dependencies{
		Stream:                    streamPublisher{stream: stream},
		ASR:                       asr,
		TTS:                       tts,
		Conversation:              conversation,
		NewVoiceDetector:          newVoiceDetector,
		BuildOggOpus:              esp32audio.BuildOggOpus,
		ExtractOpusPackets:        esp32audio.ExtractOpusPackets,
		ReencodeOpusFrames:        esp32audio.ReencodeOpusFrames,
		NormalizeImageContentType: normalizeImageContentType,
	})
	return &DeviceHub{Hub: hub, conversation: conversation, vision: vision, tts: tts}
}

func (h *DeviceHub) setConversation(pipeline *ConversationPipeline) {
	if h != nil && h.conversation != nil {
		h.conversation.pipeline = pipeline
	}
}

func (h *DeviceHub) deviceAllowed(deviceID string) bool {
	return h != nil && h.DeviceAllowed(deviceID)
}

func (h *DeviceHub) deviceAuthorized(r *http.Request) bool {
	return h != nil && h.DeviceAuthorized(r)
}

func (h *DeviceHub) Speak(ctx context.Context, deviceID string, text string) (map[string]any, error) {
	if h != nil && h.Hub != nil {
		h.Hub.SetTTS(h.tts)
	}
	return h.Hub.Speak(ctx, deviceID, text)
}

func (h *DeviceHub) CallTool(ctx context.Context, deviceID string, toolName string, args map[string]any, timeout int) (any, string, error) {
	result, err := h.Call(ctx, BridgeCallRequest{
		DeviceID:  deviceID,
		Tool:      toolName,
		Arguments: args,
		Timeout:   timeout,
	})
	if err != nil {
		return nil, "", err
	}
	return result.Result, result.Error, nil
}

type streamPublisher struct {
	stream *streamHub
}

func (p streamPublisher) Publish(event agentesp32.StreamEvent) {
	if p.stream == nil {
		return
	}
	p.stream.publish(StreamEvent{
		Type:        event.Type,
		DeviceID:    event.DeviceID,
		ContentType: event.ContentType,
		Image:       event.Image,
		Size:        event.Size,
		TS:          event.TS,
		StreamID:    event.StreamID,
		Seq:         event.Seq,
		TimestampMS: event.TimestampMS,
	})
}

type deviceConversationAdapter struct {
	pipeline *ConversationPipeline
}

func (a *deviceConversationAdapter) AnswerDeviceText(ctx context.Context, deviceID string, text string) (string, error) {
	if a == nil || a.pipeline == nil {
		return "我现在还没有配置语言模型。", nil
	}
	reply, err := a.pipeline.Run(ctx, DeviceVoiceFactory{}.Build(deviceID, text))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(reply.Text), nil
}

func newVoiceDetector() (agentesp32.VoiceDetector, error) {
	return esp32audio.NewSileroVAD()
}

type SpeechSynthesizer = agentmedia.SpeechSynthesizer

func newHTTPSpeechSynthesizer(cfg Config, client *http.Client) SpeechSynthesizer {
	return agentmedia.NewHTTPSpeechSynthesizer(agentmedia.TTSConfig{
		URL:            cfg.GoTTSURL,
		APIKey:         cfg.GoTTSAPIKey,
		Model:          cfg.GoTTSModel,
		Voice:          cfg.GoTTSVoice,
		ResponseFormat: cfg.GoTTSResponseFormat,
		Timeout:        cfg.GoTTSTimeout,
		HTTPClient:     client,
	})
}

type audioRecord struct {
	ID          string
	Token       string
	ContentType string
	Body        []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type audioStore struct {
	mu      sync.Mutex
	records map[string]audioRecord
	now     func() time.Time
	maxAge  time.Duration
}

func newAudioStore(now func() time.Time) *audioStore {
	if now == nil {
		now = time.Now
	}
	return &audioStore{
		records: map[string]audioRecord{},
		now:     now,
		maxAge:  10 * time.Minute,
	}
}

func (s *audioStore) put(contentType string, body []byte) audioRecord {
	now := s.now()
	record := audioRecord{
		ID:          randomToken(18),
		Token:       randomToken(18),
		ContentType: normalizeOggContentType(contentType),
		Body:        append([]byte(nil), body...),
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.maxAge),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, item := range s.records {
		if item.ExpiresAt.Before(now) {
			delete(s.records, id)
		}
	}
	s.records[record.ID] = record
	return record
}

func (s *audioStore) get(id string, token string) (audioRecord, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok || record.Token == "" || record.Token != token || record.ExpiresAt.Before(now) {
		return audioRecord{}, false
	}
	return record, true
}

func normalizeOggContentType(value string) string {
	return agentmedia.NormalizeOggContentType(value)
}

type ConversationChannel string

const (
	ChannelDeviceVoice ConversationChannel = "device_voice"
	ChannelLarkText    ConversationChannel = "lark_text"
	ChannelWechatText  ConversationChannel = "wechat_text"
)

type ConversationTurn struct {
	Channel        ConversationChannel
	ConversationID string
	DeviceID       string
	Text           string
	UseDeviceTools bool
	Formatter      agentchannel.ChannelFormatter
}

type ConversationReply struct {
	Text string
}

type DeviceVoiceFactory struct{}

func (DeviceVoiceFactory) Build(deviceID string, text string) ConversationTurn {
	return ConversationTurn{
		Channel:        ChannelDeviceVoice,
		ConversationID: deviceID,
		DeviceID:       deviceID,
		Text:           text,
		UseDeviceTools: true,
		Formatter:      deviceVoiceFormatter{},
	}
}

type deviceVoiceFormatter struct{}

func (deviceVoiceFormatter) Instruction() string {
	return "请用简短自然的口语回答，适合语音播报。不要使用 Markdown、表格、代码块或复杂链接格式。"
}

func (deviceVoiceFormatter) Send(context.Context, string) error {
	return nil
}

type LarkTextFactory struct{}

func (LarkTextFactory) Build(chatID string, senderID string, text string) ConversationTurn {
	return ConversationTurn{
		Channel:        ChannelLarkText,
		ConversationID: "lark:" + chatID + ":" + senderID,
		Text:           text,
	}
}

type WechatTextFactory struct{}

func (WechatTextFactory) Build(contextToken string, fromUserID string, text string) ConversationTurn {
	return ConversationTurn{
		Channel:        ChannelWechatText,
		ConversationID: "wechat:" + contextToken + ":" + fromUserID,
		Text:           text,
	}
}

type conversationChat interface {
	Chat(ctx context.Context, turn ConversationTurn) (string, error)
}

type conversationChatWithOptions interface {
	ChatWithOptions(ctx context.Context, turn ConversationTurn, opts ChatOptions) (string, error)
}

type conversationChatFunc func(ctx context.Context, turn ConversationTurn) (string, error)

func (f conversationChatFunc) Chat(ctx context.Context, turn ConversationTurn) (string, error) {
	return f(ctx, turn)
}

type ConversationPipeline struct {
	chat     conversationChat
	devices  DeviceController
	workflow *agentworkflow.Runner
}

func newConversationPipeline(agent *EinoAgent, devices DeviceController) *ConversationPipeline {
	var chat conversationChat
	if agent != nil {
		chat = einoConversationChat{agent: agent}
	}
	pipeline := &ConversationPipeline{chat: chat, devices: devices}
	registry, err := agentworkflow.NewRegistry(DefinitionChatReact())
	if err == nil {
		pipeline.workflow = agentworkflow.NewRunner(agentworkflow.RunnerConfig{
			Registry: registry,
			Agent:    conversationWorkflowAgent{pipeline: pipeline},
		})
	}
	return pipeline
}

func (p *ConversationPipeline) Run(ctx context.Context, turn ConversationTurn) (ConversationReply, error) {
	turn.Text = strings.TrimSpace(turn.Text)
	if turn.Text == "" {
		return ConversationReply{}, errors.New("conversation text is empty")
	}
	if turn.ConversationID == "" {
		turn.ConversationID = turn.DeviceID
	}
	if p.workflow != nil {
		run, err := p.workflow.Run(ctx, "chat_react", agentworkflow.Input{
			Trigger:        agentworkflow.TriggerMessage,
			Channel:        string(turn.Channel),
			ConversationID: turn.ConversationID,
			DeviceID:       turn.DeviceID,
			Text:           turn.Text,
			UseDeviceTools: turn.UseDeviceTools,
		})
		if err == nil {
			text := strings.TrimSpace(run.Output.Text)
			if text == "" {
				text = "我现在还没想好怎么回答。"
			}
			return ConversationReply{Text: text}, nil
		}
		return ConversationReply{Text: fmt.Sprintf("我现在回答不了。错误原因：%v。", err)}, nil
	}
	return p.runDirect(ctx, turn)
}

func (p *ConversationPipeline) runDirect(ctx context.Context, turn ConversationTurn) (ConversationReply, error) {
	if turn.UseDeviceTools && turn.DeviceID != "" && p.devices != nil && needsVision(turn.Text) {
		result, err := p.devices.Call(ctx, BridgeCallRequest{
			DeviceID: turn.DeviceID,
			Tool:     "self.camera.take_photo",
			Arguments: map[string]any{
				"question": turn.Text,
			},
			Timeout: 120,
		})
		if err == nil && result.Error == "" {
			if text := strings.TrimSpace(extractMCPText(result.Result)); text != "" {
				return ConversationReply{Text: text}, nil
			}
		}
		if err != nil {
			return ConversationReply{Text: "我现在看不了摄像头，原因是" + err.Error()}, nil
		}
	}
	if p.chat == nil {
		return ConversationReply{Text: "我现在还没有配置语言模型。"}, nil
	}
	answer, err := p.chat.Chat(ctx, turn)
	if err != nil {
		logger.Infof("conversation chat failed channel=%s conversation=%s device=%s: %v", turn.Channel, turn.ConversationID, turn.DeviceID, err)
		return ConversationReply{Text: fmt.Sprintf("我现在回答不了。错误原因：%v。", err)}, nil
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = "我现在还没想好怎么回答。"
	}
	return ConversationReply{Text: answer}, nil
}

func DefinitionChatReact() agentworkflow.Definition {
	return agentworkflow.Definition{
		ID:          "chat_react",
		Name:        "调度 Agent",
		Description: "由 channel 消息触发，使用 ReAct Agent 编排工具、技能和 MCP 调用并返回回复。",
		Enabled:     true,
		Trigger:     agentworkflow.Trigger{Kind: agentworkflow.TriggerMessage},
		Agent: agentworkflow.AgentSpec{
			Name:     "dispatch_agent",
			Mode:     "react",
			MaxSteps: 8,
			Timeout:  120 * time.Second,
		},
	}
}

type conversationWorkflowAgent struct {
	pipeline *ConversationPipeline
}

func (a conversationWorkflowAgent) Run(ctx context.Context, request agentworkflow.AgentRequest) (agentworkflow.AgentResponse, error) {
	if a.pipeline == nil {
		return agentworkflow.AgentResponse{}, errors.New("conversation pipeline is not configured")
	}
	turn := ConversationTurn{
		Channel:        ConversationChannel(request.Input.Channel),
		ConversationID: request.Input.ConversationID,
		DeviceID:       request.Input.DeviceID,
		Text:           request.Input.Text,
		UseDeviceTools: request.Input.UseDeviceTools,
	}
	if request.LastError != "" {
		turn.Text = strings.TrimSpace(turn.Text + "\n\n上一次执行失败：" + request.LastError + "\n请换一种方式继续完成。")
	}
	reply, err := a.pipeline.runWorkflowStep(ctx, turn, request.MaxSteps)
	if err != nil {
		return agentworkflow.AgentResponse{}, err
	}
	return agentworkflow.AgentResponse{Text: reply.Text, Finished: true}, nil
}

func (p *ConversationPipeline) runWorkflowStep(ctx context.Context, turn ConversationTurn, maxSteps int) (ConversationReply, error) {
	if turn.UseDeviceTools && turn.DeviceID != "" && p.devices != nil && needsVision(turn.Text) {
		return p.runDirect(ctx, turn)
	}
	if p.chat == nil {
		return ConversationReply{Text: "我现在还没有配置语言模型。"}, nil
	}
	if chat, ok := p.chat.(conversationChatWithOptions); ok {
		answer, err := chat.ChatWithOptions(ctx, turn, ChatOptions{MaxIterations: maxSteps})
		if err != nil {
			return ConversationReply{}, err
		}
		return ConversationReply{Text: answer}, nil
	}
	return p.runDirect(ctx, turn)
}

type einoConversationChat struct {
	agent *EinoAgent
}

func (c einoConversationChat) Chat(ctx context.Context, turn ConversationTurn) (string, error) {
	return c.agent.ChatWithContext(ctx, turn.ConversationID, turn.DeviceID, formattedUserText(turn))
}

func (c einoConversationChat) ChatWithOptions(ctx context.Context, turn ConversationTurn, opts ChatOptions) (string, error) {
	return c.agent.ChatWithContextOptions(ctx, turn.ConversationID, turn.DeviceID, formattedUserText(turn), opts)
}

func formattedUserText(turn ConversationTurn) string {
	if turn.Formatter == nil {
		return turn.Text
	}
	instruction := strings.TrimSpace(turn.Formatter.Instruction())
	if instruction == "" {
		return turn.Text
	}
	return "回复格式要求：\n" + instruction + "\n\n用户问题：\n" + turn.Text
}

type larkCallback = agentlark.Callback

type larkMessageEvent = agentlark.MessageEvent

func (s *AdminServer) handleLarkEvents(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.LarkEnabled() {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024))
	if err != nil {
		logger.Infof("[lark] event request read failed: %v", err)
		http.Error(w, "read event failed", http.StatusBadRequest)
		return
	}
	var callback larkCallback
	if err := json.Unmarshal(raw, &callback); err != nil {
		logger.Infof("[lark] event request invalid json bytes=%d: %v", len(raw), err)
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	eventType := callback.EventType()
	logger.Infof("[lark] event received type=%s event_id=%s app_id=%s tenant=%s schema=%s bytes=%d", eventType, callback.Header.EventID, callback.Header.AppID, callback.Header.TenantKey, callback.Schema, len(raw))
	if callback.Header.AppID != "" && callback.Header.AppID != s.cfg.LarkAppID {
		logger.Infof("[lark] event rejected: app_id mismatch got=%s want=%s type=%s event_id=%s", callback.Header.AppID, s.cfg.LarkAppID, eventType, callback.Header.EventID)
		http.Error(w, "app id mismatch", http.StatusForbidden)
		return
	}
	switch eventType {
	case agentlark.EventTypeURLVerification:
		challenge := callback.URLVerificationChallenge()
		logger.Infof("[lark] url verification received event_id=%s challenge_present=%v", callback.Header.EventID, challenge != "")
		writeJSON(w, http.StatusOK, map[string]string{"challenge": challenge})
	case agentlark.EventTypeMessageReceive:
		if callback.Header.EventID != "" && s.larkEventSeen(callback.Header.EventID) {
			logger.Infof("[lark] event duplicate ignored type=%s event_id=%s", eventType, callback.Header.EventID)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "duplicate": true})
			return
		}
		if err := s.handleLarkTextMessage(r.Context(), callback); err != nil {
			logger.Infof("[lark] message handling failed event_id=%s: %v", callback.Header.EventID, err)
			writeJSON(w, http.StatusOK, map[string]any{"ok": false})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		logger.Infof("[lark] event ignored type=%s event_id=%s", eventType, callback.Header.EventID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
	}
}

var larkProcessingEmojis = []string{"Typing", "OnIt", "THINKING", "OneSecond"}

func pickLarkReaction() string {
	return larkProcessingEmojis[rand.Intn(len(larkProcessingEmojis))]
}

func (s *AdminServer) handleLarkTextMessage(ctx context.Context, callback larkCallback) error {
	var event larkMessageEvent
	if err := json.Unmarshal(callback.Event, &event); err != nil {
		return fmt.Errorf("decode message event: %w", err)
	}
	senderID := event.SenderID()
	logger.Infof("[lark] message received event_id=%s chat=%s message=%s sender_type=%s sender=%s message_type=%s content_bytes=%d", callback.Header.EventID, event.Message.ChatID, event.Message.MessageID, event.Sender.SenderType, senderID, event.Message.MessageType, len(event.Message.Content))
	if event.Sender.SenderType == "bot" {
		logger.Infof("[lark] message ignored event_id=%s reason=bot_sender message=%s", callback.Header.EventID, event.Message.MessageID)
		return nil
	}
	if event.Message.MessageType == "image" {
		return s.handleLarkImageMessage(ctx, callback, event, senderID)
	}
	if event.Message.MessageType != "text" {
		logger.Infof("[lark] message ignored event_id=%s reason=non_text message=%s message_type=%s", callback.Header.EventID, event.Message.MessageID, event.Message.MessageType)
		return nil
	}
	text := event.Text()
	if text == "" {
		logger.Infof("[lark] message ignored event_id=%s reason=empty_text message=%s", callback.Header.EventID, event.Message.MessageID)
		return nil
	}
	if event.Message.ChatID == "" || senderID == "" || event.Message.MessageID == "" {
		return fmt.Errorf("message event missing chat, sender, or message id")
	}
	if reply, ok := s.handleBuiltinCommand(ctx, ChannelLarkText, text); ok {
		formatter := agentlark.NewReplyFormatter(s.newLarkClient(), event.Message.MessageID)
		if err := formatter.Send(ctx, reply); err != nil {
			return err
		}
		logger.Infof("[lark] builtin command reply sent event_id=%s message=%s chat=%s", callback.Header.EventID, event.Message.MessageID, event.Message.ChatID)
		return nil
	}
	if s.conversation == nil {
		return fmt.Errorf("conversation pipeline is not configured")
	}

	lc := s.newLarkClient()
	formatter := agentlark.NewReplyFormatter(lc, event.Message.MessageID)
	emojiType := pickLarkReaction()
	reactionID, err := lc.AddReaction(ctx, event.Message.MessageID, emojiType)
	if err != nil {
		logger.Infof("[lark] add reaction failed event_id=%s message=%s emoji=%s err=%v", callback.Header.EventID, event.Message.MessageID, emojiType, err)
	}
	if reactionID != "" {
		defer func() {
			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanCancel()
			if err := lc.RemoveReaction(cleanCtx, event.Message.MessageID, reactionID); err != nil {
				logger.Infof("[lark] remove reaction failed event_id=%s message=%s emoji=%s err=%v", callback.Header.EventID, event.Message.MessageID, emojiType, err)
			}
		}()
	}

	turn := LarkTextFactory{}.Build(event.Message.ChatID, senderID, text)
	turn.Formatter = formatter
	reply, err := s.conversation.Run(ctx, turn)
	if err != nil {
		logger.Infof("[lark] conversation error: %v", err)
	}
	if reply.Text == "" {
		return fmt.Errorf("lark conversation returned empty reply")
	}
	if err := formatter.Send(ctx, reply.Text); err != nil {
		return err
	}
	logger.Infof("[lark] message reply sent event_id=%s message=%s chat=%s", callback.Header.EventID, event.Message.MessageID, event.Message.ChatID)
	return nil
}

func (s *AdminServer) handleLarkImageMessage(ctx context.Context, callback larkCallback, event larkMessageEvent, senderID string) error {
	if event.Message.ChatID == "" || senderID == "" || event.Message.MessageID == "" {
		return fmt.Errorf("image message event missing chat, sender, or message id")
	}
	imageKey := event.ImageKey()
	if imageKey == "" {
		logger.Infof("[lark] image message ignored event_id=%s reason=missing_image_key message=%s", callback.Header.EventID, event.Message.MessageID)
		return nil
	}
	if s.deviceHub == nil || s.deviceHub.vision == nil {
		return fmt.Errorf("vision model is not configured")
	}
	lc := s.newLarkClient()
	formatter := agentlark.NewReplyFormatter(lc, event.Message.MessageID)
	contentType, body, err := lc.DownloadImage(ctx, event.Message.MessageID, imageKey)
	if err != nil {
		return err
	}
	if len(body) > 2*1024*1024 {
		return fmt.Errorf("lark image too large: %d bytes", len(body))
	}
	answer, err := s.deviceHub.vision.Analyze(ctx, "请描述这张图片里的内容。", contentType, body)
	if err != nil {
		return fmt.Errorf("vision model failed: %w", err)
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = "我没有看清这张图片。"
	}
	if err := formatter.Send(ctx, answer); err != nil {
		return err
	}
	logger.Infof("[lark] image reply sent event_id=%s message=%s chat=%s bytes=%d", callback.Header.EventID, event.Message.MessageID, event.Message.ChatID, len(body))
	return nil
}

func (s *AdminServer) larkEventSeen(eventID string) bool {
	if eventID == "" {
		return false
	}
	now := s.cfg.now()
	s.larkMu.Lock()
	defer s.larkMu.Unlock()
	for id, seenAt := range s.larkEvents {
		if now.Sub(seenAt) > time.Hour {
			delete(s.larkEvents, id)
		}
	}
	if _, ok := s.larkEvents[eventID]; ok {
		return true
	}
	s.larkEvents[eventID] = now
	return false
}

func (s *AdminServer) newLarkClient() *agentlark.Client {
	return agentlark.NewClient(agentlark.ClientConfig{
		AppID:      s.cfg.LarkAppID,
		AppToken:   s.cfg.LarkAppToken,
		HTTPClient: s.httpClient,
	})
}

func (s *AdminServer) startLarkWSClient(ctx context.Context) {
	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Sender == nil {
				return nil
			}
			eventID := ""
			if event.EventV2Base != nil && event.EventV2Base.Header != nil {
				eventID = event.EventV2Base.Header.EventID
			}
			if eventID != "" && s.larkEventSeen(eventID) {
				logger.Infof("[lark] ws event duplicate ignored event_id=%s", eventID)
				return nil
			}

			msg := event.Event.Message
			sender := event.Event.Sender

			msgEvent := larkMessageEvent{}
			msgEvent.Sender.SenderType = safeString(sender.SenderType)
			if sender.SenderId != nil {
				msgEvent.Sender.SenderID.OpenID = safeString(sender.SenderId.OpenId)
				msgEvent.Sender.SenderID.UserID = safeString(sender.SenderId.UserId)
				msgEvent.Sender.SenderID.UnionID = safeString(sender.SenderId.UnionId)
			}
			msgEvent.Message.MessageID = safeString(msg.MessageId)
			msgEvent.Message.ChatID = safeString(msg.ChatId)
			msgEvent.Message.MessageType = safeString(msg.MessageType)
			msgEvent.Message.Content = safeString(msg.Content)

			eventBytes, _ := json.Marshal(msgEvent)

			callback := larkCallback{}
			if event.EventV2Base != nil && event.EventV2Base.Header != nil {
				callback.Header.EventID = event.EventV2Base.Header.EventID
				callback.Header.EventType = event.EventV2Base.Header.EventType
				callback.Header.AppID = event.EventV2Base.Header.AppID
				callback.Header.TenantKey = event.EventV2Base.Header.TenantKey
			}
			callback.Event = eventBytes

			handleCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			return s.handleLarkTextMessage(handleCtx, callback)
		})

	cli := larkws.NewClient(s.cfg.LarkAppID, s.cfg.LarkAppToken,
		larkws.WithEventHandler(eventHandler),
		larkws.WithDomain(lark.LarkBaseUrl),
		larkws.WithLogLevel(larkcore.LogLevelWarn),
	)

	logger.Infof("[lark] ws start app_id=%s", s.cfg.LarkAppID)
	if err := cli.Start(ctx); err != nil {
		logger.Infof("[lark] ws stopped: %v", err)
	}
}

func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

const (
	wechatDefaultBaseURL = agentwechat.DefaultBaseURL
)

type wechatMessage = agentwechat.Message

type wechatQRCodeStatus = agentwechat.QRCodeStatus

type wechatClient = agentwechat.Client

func newWechatClient() *wechatClient {
	return agentwechat.NewClient(agentwechat.ClientConfig{})
}

func (s *AdminServer) startWechatPolling(ctx context.Context) {
	token := s.cfg.WeChatBotToken
	if token == "" {
		logger.Infof("[wechat] WECHAT_BOT_TOKEN not set, skipping")
		return
	}

	c := newWechatClient()
	c.Token = token
	c.BaseURL = s.cfg.WeChatBaseURL

	agentwechat.PollMessages(ctx, c, func(ctx context.Context, msg *wechatMessage) {
		s.handleWechatMessage(ctx, c, msg)
	})
}

func (s *AdminServer) handleWechatMessage(ctx context.Context, c *wechatClient, msg *wechatMessage) {
	text := strings.TrimSpace(msg.Text())
	if text == "" {
		return
	}
	logger.Infof("[wechat] message from=%s text=%q", msg.FromUserID, text)

	if msg.ContextToken == "" || msg.FromUserID == "" || msg.ToUserID == "" {
		logger.Infof("[wechat] message missing context_token/from/to, ignored")
		return
	}

	if reply, ok := s.handleBuiltinCommand(ctx, ChannelWechatText, text); ok {
		formatter := agentwechat.NewReplyFormatter(c, msg.ToUserID, msg.FromUserID, msg.ContextToken)
		if err := formatter.Send(ctx, reply); err != nil {
			logger.Infof("[wechat] builtin command send error: %v", err)
		} else {
			logger.Infof("[wechat] builtin command send ok to=%s command=%q", msg.FromUserID, text)
		}
		return
	}

	if s.conversation == nil {
		logger.Infof("[wechat] conversation not configured")
		return
	}

	if err := wechatSendTyping(ctx, c, msg.ToUserID, msg.FromUserID, msg.ContextToken); err != nil {
		logger.Infof("[wechat] typing send error: %v", err)
	} else {
		logger.Infof("[wechat] typing send ok to=%s", msg.FromUserID)
	}

	formatter := agentwechat.NewReplyFormatter(c, msg.ToUserID, msg.FromUserID, msg.ContextToken)
	turn := WechatTextFactory{}.Build(msg.ContextToken, msg.FromUserID, text)
	turn.Formatter = formatter
	reply, err := s.conversation.Run(ctx, turn)
	if err != nil {
		logger.Infof("[wechat] conversation error: %v", err)
	}
	if reply.Text == "" {
		reply = ConversationReply{Text: "抱歉，我暂时无法回答。"}
	}

	if err := formatter.Send(ctx, reply.Text); err != nil {
		logger.Infof("[wechat] send error: %v", err)
	} else {
		logger.Infof("[wechat] send ok to=%s text=%q", msg.FromUserID, reply.Text)
	}
}

func wechatSendTyping(ctx context.Context, c *wechatClient, fromUserID, toUserID, contextToken string) error {
	return agentwechat.SendTyping(ctx, c, fromUserID, toUserID, contextToken)
}

func wechatSendText(ctx context.Context, c *wechatClient, fromUserID, toUserID, contextToken, text string) error {
	return agentwechat.SendText(ctx, c, fromUserID, toUserID, contextToken, text)
}

func wechatLogin(ctx context.Context, onQRCode func(content string)) error {
	return agentwechat.Login(ctx, onQRCode, func(status wechatQRCodeStatus) {
		switch status.Status {
		case "confirmed":
			logger.Infof("[wechat] login successful!")
			fmt.Printf("\nWECHAT_BOT_TOKEN=%s\nWECHAT_BASE_URL=%s\n", status.BotToken, status.BaseURL)
		case "scaned":
			logger.Infof("[wechat] QR scanned, waiting for confirmation")
		}
	})
}

func WechatLoginCLI(ctx context.Context) error {
	fmt.Println("正在获取二维码，请用微信扫码登录...")
	return wechatLogin(ctx, func(content string) {
		fmt.Println("扫码内容:", content)
	})
}

const studyMonitorPrompt = `请检查这张照片中孩子的学习状态，重点判断：
1. 坐姿是否端正，是否趴桌、歪斜、低头过近或离座；
2. 是否正在认真学习，是否明显走神、玩东西或看无关内容；
3. 如果需要提醒，请只针对坐姿或学习状态给出简短提醒。

请尽量返回 JSON：
{"need_reminder": true/false, "posture": "...", "focus": "...", "summary": "...", "reminder_text": "..."}
`

var studyProblemKeywords = []string{
	"坐姿有问题", "趴", "趴桌", "歪", "歪斜", "低头", "过近", "离座", "走神", "分心", "玩东西", "玩手机", "不认真", "需要提醒",
}

var studyNegationKeywords = []string{"没有明显问题", "未发现问题", "坐姿端正", "认真学习", "无需提醒", "不需要提醒"}

type studyDecision struct {
	NeedReminder bool
	AnalysisText string
	ReminderText string
}

func (s *AdminServer) StartBackground(ctx context.Context) {
	if s.cfg.LarkEnabled() {
		go s.startLarkWSClient(ctx)
	} else {
		logger.Infof("[lark] ws skipped app_id_configured=%v token_configured=%v",
			strings.TrimSpace(s.cfg.LarkAppID) != "",
			strings.TrimSpace(s.cfg.LarkAppToken) != "")
	}
	if s.cfg.WeChatEnabled {
		go s.startWechatPolling(ctx)
	}
	go s.startWorkflowCronScheduler(ctx)
}

func (s *AdminServer) studyMonitorSlot(checkedAt time.Time) *int64 {
	return agentworkflow.CronSlot(s.studyMonitorCronSpec(), checkedAt)
}

func (s *AdminServer) inStudyMonitorWindow(checkedAt time.Time) bool {
	return agentworkflow.InWindow(s.studyMonitorCronSpec(), checkedAt)
}

func (s *AdminServer) morningGreetingSlot(checkedAt time.Time) *int64 {
	return agentworkflow.CronSlot(s.morningGreetingCronSpec(), checkedAt)
}

func (s *AdminServer) startWorkflowCronScheduler(ctx context.Context) {
	jobs := []agentworkflow.CronJob{}
	for _, def := range s.workflowDefinitions() {
		switch def.ID {
		case "study_monitor":
			jobs = append(jobs, agentworkflow.CronJob{Definition: def, Run: s.runStudyMonitorOnce})
		case "morning_greeting":
			jobs = append(jobs, agentworkflow.CronJob{
				Definition: def,
				Run: func(ctx context.Context, scheduledAt time.Time) error {
					if err := s.runMorningGreetingOnce(ctx, scheduledAt); err != nil {
						logger.Infof("morning greeting failed: %v", err)
						return err
					}
					return nil
				},
			})
		}
	}
	if len(jobs) == 0 {
		return
	}
	agentworkflow.NewCronScheduler(agentworkflow.CronSchedulerConfig{
		Jobs: jobs,
		Tick: 30 * time.Second,
		Now:  s.cfg.now,
	}).Start(ctx)
}

func (s *AdminServer) studyMonitorCronSpec() agentworkflow.CronSpec {
	return agentworkflow.CronSpec{
		Every:     s.cfg.StudyMonitorInterval,
		Timezone:  s.cfg.StudyMonitorTimezone,
		StartHour: s.cfg.StudyMonitorStartHour,
		EndHour:   s.cfg.StudyMonitorEndHour,
	}
}

func (s *AdminServer) morningGreetingCronSpec() agentworkflow.CronSpec {
	hour := clampInt(s.cfg.MorningGreetingHour, 0, 23, 8)
	minute := clampInt(s.cfg.MorningGreetingMinute, 0, 59, 0)
	return agentworkflow.CronSpec{
		Timezone: s.cfg.MorningGreetingTimezone,
		AtHour:   &hour,
		AtMinute: &minute,
	}
}

func (s *AdminServer) runMorningGreetingOnce(ctx context.Context, checkedAt time.Time) error {
	controller := s.deviceController()
	devices, err := controller.Devices(ctx)
	if err != nil {
		return err
	}
	deviceID := pickOnlineDevice(devices, s.cfg.MorningGreetingDeviceIDs)
	if deviceID == "" {
		logger.Infof("morning greeting skipped at %s: no eligible device (allowlist=%v, online=%d)", checkedAt.Format(time.RFC3339), s.cfg.MorningGreetingDeviceIDs, len(devices))
		return nil
	}

	text := s.dailyEncouragement(ctx)
	if text == "" {
		text = strings.TrimSpace(s.cfg.MorningGreetingText)
	}
	if text == "" {
		text = "早上好。"
	}

	_, err = controller.Speak(ctx, deviceID, text)
	if err != nil {
		return err
	}
	logger.Infof("morning greeting played for %s at %s: %q", deviceID, checkedAt.Format(time.RFC3339), text)
	return nil
}

func (s *AdminServer) dailyEncouragement(ctx context.Context) string {
	if s.agent == nil {
		return ""
	}

	// Fetch the skill prompt from the MCP server
	promptText := s.fetchMCPPrompt(ctx, "daily-encouragement")
	if promptText == "" {
		return ""
	}

	// Let the LLM generate the greeting — it has all external MCP tools available
	// and can decide whether to call curl for weather, holiday info, etc.
	userMsg := fmt.Sprintf(
		"今天的日期是 %s。请根据上面的规则生成今日鼓励，只返回一句话。",
		s.cfg.now().Format("2006年1月2日 周一"),
	)
	greeting, err := s.agent.Generate(ctx, promptText, userMsg)
	if err != nil {
		logger.Infof("daily encouragement: generate error: %v", err)
		return ""
	}
	return greeting
}

func (s *AdminServer) fetchMCPPrompt(ctx context.Context, promptName string) string {
	urls := s.cfg.ExternalMCPURLs
	if len(urls) == 0 {
		return ""
	}

	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"xiaoli-server","version":"1.0"}}}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, urls[0], strings.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ""
	}
	sessionID := resp.Header.Get("Mcp-Session-Id")
	resp.Body.Close()
	if sessionID == "" {
		return ""
	}

	getBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"%s"}}`, promptName)
	req2, _ := http.NewRequestWithContext(ctx, http.MethodPost, urls[0], strings.NewReader(getBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept", "application/json, text/event-stream")
	req2.Header.Set("Mcp-Session-Id", sessionID)
	resp2, err := s.httpClient.Do(req2)
	if err != nil {
		return ""
	}
	defer resp2.Body.Close()
	raw, _ := io.ReadAll(resp2.Body)
	bodyStr := string(raw)
	if idx := strings.Index(bodyStr, "data: "); idx >= 0 {
		bodyStr = bodyStr[idx+6:]
	}

	var result struct {
		Result *struct {
			Messages []struct {
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(bodyStr), &result); err != nil {
		return ""
	}
	if result.Result == nil || len(result.Result.Messages) == 0 {
		return ""
	}
	return result.Result.Messages[0].Content.Text
}

func pickOnlineDevice(devices []Device, allowlist []string) string {
	if len(allowlist) == 0 {
		if len(devices) == 0 {
			return ""
		}
		return devices[0].DeviceID
	}
	for _, d := range devices {
		for _, allowed := range allowlist {
			if d.DeviceID == allowed {
				return d.DeviceID
			}
		}
	}
	return ""
}

func (s *AdminServer) runStudyMonitorOnce(ctx context.Context, checkedAt time.Time) error {
	controller := s.deviceController()
	devices, err := controller.Devices(ctx)
	if err != nil {
		return err
	}
	deviceID := pickOnlineDevice(devices, s.cfg.StudyMonitorDeviceIDs)
	if deviceID == "" {
		logger.Infof("study monitor skipped at %s: no eligible device (allowlist=%v, online=%d)", checkedAt.Format(time.RFC3339), s.cfg.StudyMonitorDeviceIDs, len(devices))
		return nil
	}
	started := s.cfg.now()
	result, err := controller.Call(ctx, BridgeCallRequest{
		DeviceID:  deviceID,
		Tool:      s.cfg.StudyMonitorCameraTool,
		Arguments: map[string]any{"question": studyMonitorPrompt},
		Timeout:   int(s.cfg.StudyMonitorToolTimeout.Seconds()),
	})
	if err != nil {
		return err
	}
	decision := s.parseStudyDecision(result.Result)
	reminderResult := ""
	if decision.NeedReminder {
		if response, err := controller.Speak(ctx, deviceID, decision.ReminderText); err == nil {
			encoded, _ := json.Marshal(response)
			reminderResult = string(encoded)
		} else {
			reminderResult = err.Error()
		}
	}
	imageKey := ""
	if record := s.recentDeviceImageRecord(deviceID, started.Add(-2*time.Second)); record != nil {
		logger.Infof("[lark] found device image for %s: bytes=%d content-type=%s", deviceID, len(record.Body), record.ContentType)
		if key, err := s.uploadLarkImage(ctx, record.Body, record.ContentType); err == nil {
			imageKey = key
		} else {
			logger.Infof("[lark] image upload failed for device=%s: %v", deviceID, err)
		}
	} else {
		logger.Infof("[lark] no device image found for %s", deviceID)
	}
	return s.sendLarkStudyMessage(ctx, studyLarkPayloadInput{
		DeviceID:       deviceID,
		AnalysisText:   decision.AnalysisText,
		NeedReminder:   decision.NeedReminder,
		ReminderText:   decision.ReminderText,
		ImageKey:       imageKey,
		CheckedAt:      checkedAt,
		ReminderResult: reminderResult,
		ElapsedMS:      result.ElapsedMS,
	})
}

func (s *AdminServer) parseStudyDecision(value any) studyDecision {
	parsed := s.extractStudyDecisionPayload(value)
	if payload, ok := parsed.(map[string]any); ok {
		var textParts []string
		for _, key := range []string{"summary", "posture", "focus", "response", "analysis", "message", "text"} {
			if text := strings.TrimSpace(stringValue(payload[key])); text != "" && text != "<nil>" {
				textParts = append(textParts, text)
			}
		}
		analysisText := strings.Join(textParts, "\n")
		if analysisText == "" {
			encoded, _ := json.Marshal(payload)
			analysisText = string(encoded)
		}
		needReminder, ok := payload["need_reminder"].(bool)
		if !ok {
			needReminder, ok = payload["needReminder"].(bool)
		}
		if !ok {
			needReminder, ok = payload["remind"].(bool)
		}
		if !ok {
			needReminder = studyTextNeedsReminder(analysisText)
		}
		reminderText := strings.TrimSpace(stringValue(payload["reminder_text"]))
		if reminderText == "" || reminderText == "<nil>" {
			reminderText = strings.TrimSpace(stringValue(payload["reminder"]))
		}
		if reminderText == "" || reminderText == "<nil>" {
			reminderText = s.cfg.StudyMonitorReminder
		}
		return studyDecision{NeedReminder: needReminder, AnalysisText: analysisText, ReminderText: reminderText}
	}
	analysisText := strings.TrimSpace(stringValue(parsed))
	return studyDecision{
		NeedReminder: studyTextNeedsReminder(analysisText),
		AnalysisText: analysisText,
		ReminderText: s.cfg.StudyMonitorReminder,
	}
}

func (s *AdminServer) extractStudyDecisionPayload(value any) any {
	parsed := tryJSONValue(value)
	if payload, ok := parsed.(map[string]any); ok {
		for _, key := range []string{"need_reminder", "needReminder", "remind"} {
			if _, ok := payload[key]; ok {
				return payload
			}
		}
		for _, key := range []string{"response", "result", "text", "message", "answer"} {
			if child, ok := payload[key]; ok && child != nil {
				extracted := s.extractStudyDecisionPayload(child)
				if _, ok := extracted.(map[string]any); ok {
					return extracted
				}
			}
		}
		if content, ok := payload["content"].([]any); ok {
			for _, item := range content {
				extracted := s.extractStudyDecisionPayload(item)
				if _, ok := extracted.(map[string]any); ok {
					return extracted
				}
			}
		}
		return payload
	}
	if items, ok := parsed.([]any); ok {
		for _, item := range items {
			extracted := s.extractStudyDecisionPayload(item)
			if _, ok := extracted.(map[string]any); ok {
				return extracted
			}
		}
	}
	return parsed
}

func tryJSONValue(value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return value
	}
	return parsed
}

func studyTextNeedsReminder(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, item := range studyNegationKeywords {
		if strings.Contains(text, item) {
			return false
		}
	}
	for _, item := range studyProblemKeywords {
		if strings.Contains(text, item) {
			return true
		}
	}
	return false
}

type studyLarkPayloadInput struct {
	DeviceID       string
	AnalysisText   string
	NeedReminder   bool
	ReminderText   string
	ImageKey       string
	CheckedAt      time.Time
	ReminderResult string
	ElapsedMS      int
}

func (s *AdminServer) buildLarkPostPayload(input studyLarkPayloadInput) map[string]any {
	status := "状态正常"
	if input.NeedReminder {
		status = "需要提醒"
	}
	lines := [][]map[string]string{
		{{"tag": "text", "text": "设备：" + input.DeviceID}},
		{{"tag": "text", "text": "结论：" + status}},
		{{"tag": "text", "text": "解读：" + firstText(input.AnalysisText, "无")}},
	}
	if input.NeedReminder {
		lines = append(lines, []map[string]string{{"tag": "text", "text": "已提醒：" + input.ReminderText}})
	}
	if input.ReminderResult != "" {
		lines = append(lines, []map[string]string{{"tag": "text", "text": "喇叭调用：" + truncate(input.ReminderResult, 120)}})
	}
	if input.ElapsedMS > 0 {
		lines = append(lines, []map[string]string{{"tag": "text", "text": fmt.Sprintf("耗时：%dms", input.ElapsedMS)}})
	}
	if input.ImageKey != "" {
		lines = append(lines, []map[string]string{{"tag": "img", "image_key": input.ImageKey}})
	} else {
		lines = append(lines, []map[string]string{{"tag": "text", "text": "图片：未上传成功"}})
	}
	return map[string]any{
		"msg_type": "post",
		"content": map[string]any{
			"post": map[string]any{
				"zh_cn": map[string]any{
					"title":   "学习状态巡检 " + input.CheckedAt.Format("2006-01-02 15:04"),
					"content": lines,
				},
			},
		},
	}
}

func (s *AdminServer) sendLarkStudyMessage(ctx context.Context, input studyLarkPayloadInput) error {
	if s.cfg.LarkWebhookURL == "" {
		return nil
	}
	payload := s.buildLarkPostPayload(input)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.LarkWebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("lark webhook failed: %d %s", resp.StatusCode, string(text))
	}
	return nil
}

func (s *AdminServer) uploadLarkImage(ctx context.Context, body []byte, contentType string) (string, error) {
	if s.cfg.LarkAppID == "" || s.cfg.LarkAppToken == "" {
		logger.Infof("[lark] image upload skipped: Lark credentials not configured")
		return "", nil
	}
	logger.Infof("[lark] image upload start: bytes=%d content-type=%s", len(body), contentType)
	token, err := s.getLarkTenantAccessToken(ctx)
	if err != nil {
		logger.Infof("[lark] image upload failed: get tenant access token: %v", err)
		return "", err
	}
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	_ = writer.WriteField("image_type", "message")
	part, err := writer.CreateFormFile("image", "study-monitor.jpg")
	if err != nil {
		logger.Infof("[lark] image upload failed: create form file: %v", err)
		return "", err
	}
	_, _ = part.Write(body)
	_ = writer.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.larksuite.com/open-apis/im/v1/images", &form)
	if err != nil {
		logger.Infof("[lark] image upload failed: create request: %v", err)
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Image-Content-Type", contentType)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		logger.Infof("[lark] image upload failed: HTTP request: %v", err)
		return "", err
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		logger.Infof("[lark] image upload failed: decode response: %v", err)
		return "", err
	}
	if code, _ := int64Value(payload["code"]); code != 0 {
		logger.Infof("[lark] image upload failed: API error code=%v msg=%v", payload["code"], payload["msg"])
		return "", fmt.Errorf("lark image upload failed: %v", payload)
	}
	data, _ := payload["data"].(map[string]any)
	imageKey := stringValue(data["image_key"])
	logger.Infof("[lark] image upload ok: image_key=%s", imageKey)
	return imageKey, nil
}

func (s *AdminServer) getLarkTenantAccessToken(ctx context.Context) (string, error) {
	requestBody, _ := json.Marshal(map[string]string{"app_id": s.cfg.LarkAppID, "app_secret": s.cfg.LarkAppToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.larksuite.com/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(requestBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if code, _ := int64Value(payload["code"]); code != 0 {
		return "", fmt.Errorf("lark tenant_access_token failed: %v", payload)
	}
	return stringValue(payload["tenant_access_token"]), nil
}

func (s *AdminServer) recentDeviceImageRecord(deviceID string, since time.Time) *imageRecord {
	s.imagesMu.Lock()
	defer s.imagesMu.Unlock()
	ids := s.imagesByDev[deviceID]
	for i := len(ids) - 1; i >= 0; i-- {
		record, ok := s.images[ids[i]]
		if !ok {
			continue
		}
		if record.CreatedAt.Before(since) {
			break
		}
		copyRecord := record
		return &copyRecord
	}
	return nil
}

func firstText(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
