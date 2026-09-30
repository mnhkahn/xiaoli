package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
)

// Voice turns deliberately have their own small memory and tool set. Text
// channels continue to use ConversationPipeline and its full Agent state.
type voiceConversation struct {
	model   *openai.ChatModel
	modelID string
	devices DeviceController
	mu      sync.Mutex
	history map[string][]*schema.Message
}

var voiceTools = map[string]string{
	"get_device_status": "self.get_device_status",
	"set_volume":        "self.audio_speaker.set_volume",
	"set_brightness":    "self.screen.set_brightness",
	"set_theme":         "self.screen.set_theme",
	"take_photo":        "self.camera.take_photo",
}

func newVoiceConversation(cfg Config) *voiceConversation {
	v := &voiceConversation{history: make(map[string][]*schema.Message)}
	selected := cfg.GoLLMModel
	if cfg.GoVoiceLLMModel != "" {
		selected = cfg.GoVoiceLLMModel
	}
	modelURL, apiKey, modelName := cfg.GoLLMURL, cfg.GoLLMAPIKey, selected
	if model, ok := cfg.GoLLMModelConfigs[selected]; ok {
		modelURL, apiKey = model.BaseURL, model.APIKey
		if model.Model != "" {
			modelName = model.Model
		}
	} else if cfg.GoVoiceLLMModel != "" {
		logger.Infof("voice model %q is not in the configured catalog", selected)
		return v
	}
	if modelURL == "" || apiKey == "" || modelName == "" {
		return v
	}
	maxTokens := 256
	temp := float32(0.2)
	model, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: strings.TrimRight(strings.TrimSuffix(modelURL, "/chat/completions"), "/"),
		APIKey:  apiKey, Model: modelName,
		MaxTokens: &maxTokens, Temperature: &temp,
		HTTPClient: &http.Client{Timeout: 25 * time.Second},
	})
	if err != nil {
		logger.Infof("voice model unavailable: %v", err)
		return v
	}
	v.model = model
	v.modelID = selected
	return v
}

func (v *voiceConversation) messages(deviceID, text string) []*schema.Message {
	v.mu.Lock()
	defer v.mu.Unlock()
	prior := v.history[deviceID]
	if len(prior) > 4 {
		prior = prior[len(prior)-4:]
	}
	messages := []*schema.Message{schema.SystemMessage("你是小李智能音响。用自然、简短的中文口语回答，先直接回答问题。不要输出思考过程、Markdown、链接或工具细节。需要设备控制时调用可用工具。没有外部工具时，不要编造实时信息；说明需要稍后查询。每次回答尽量不超过两句。")}
	messages = append(messages, prior...)
	return append(messages, schema.UserMessage(text))
}

func (v *voiceConversation) remember(deviceID, question, answer string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	items := append(v.history[deviceID], schema.UserMessage(question), schema.AssistantMessage(answer, nil))
	if len(items) > 4 {
		items = items[len(items)-4:]
	}
	v.history[deviceID] = items
}

func (v *voiceConversation) availableTools(ctx context.Context, deviceID string) []*schema.ToolInfo {
	if v.devices == nil {
		return nil
	}
	listed, err := v.devices.Tools(ctx, deviceID)
	if err != nil || !listed.Ready {
		return nil
	}
	var out []*schema.ToolInfo
	for _, tool := range listed.Tools {
		name, _ := tool["name"].(string)
		for alias, original := range voiceTools {
			if name != original {
				continue
			}
			desc, _ := tool["description"].(string)
			info := &schema.ToolInfo{Name: alias, Desc: desc}
			switch alias {
			case "set_volume":
				info.ParamsOneOf = schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"volume": {Type: schema.Integer, Required: true, Desc: "音量 0 到 100"}})
			case "set_brightness":
				info.ParamsOneOf = schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"brightness": {Type: schema.Integer, Required: true, Desc: "亮度 0 到 100"}})
			case "set_theme":
				info.ParamsOneOf = schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"theme": {Type: schema.String, Required: true, Enum: []string{"light", "dark"}}})
			case "take_photo":
				info.ParamsOneOf = schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"question": {Type: schema.String, Required: true}})
			}
			out = append(out, info)
		}
	}
	return out
}

func (v *voiceConversation) AnswerDeviceTextStream(ctx context.Context, deviceID, text string, emit func(string) error) error {
	if v == nil || v.model == nil {
		return emit("我现在还没有配置语言模型。")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	started := time.Now()
	messages := v.messages(deviceID, text)
	tools := v.availableTools(ctx, deviceID)
	var chat model.ToolCallingChatModel = v.model
	if len(tools) > 0 {
		var err error
		chat, err = v.model.WithTools(tools)
		if err != nil {
			logger.Infof("voice tools unavailable for %s: %v", deviceID, err)
			chat = v.model
		}
	}
	answer, called, err := v.stream(ctx, chat, messages, emit, deviceID, 1, len(tools))
	if err != nil {
		return err
	}
	if len(called) > 0 {
		call := called[0] // one device action per turn
		original, allowed := voiceTools[call.Function.Name]
		if !allowed || !voiceToolOffered(tools, call.Function.Name) {
			return fmt.Errorf("unavailable voice tool %q", call.Function.Name)
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return err
		}
		if args == nil {
			args = map[string]any{}
		}
		result, callErr := v.devices.Call(ctx, BridgeCallRequest{DeviceID: deviceID, Tool: original, Arguments: args, Timeout: 8})
		toolText := "成功"
		if callErr != nil {
			toolText = callErr.Error()
		} else if result.Error != "" {
			toolText = result.Error
		} else if result.Result != nil {
			toolText = fmt.Sprint(result.Result)
		}
		if len(toolText) > 1000 {
			toolText = toolText[:1000]
		}
		messages = append(messages, schema.AssistantMessage(answer, []schema.ToolCall{call}), schema.ToolMessage(toolText, call.ID))
		answer, _, err = v.stream(ctx, v.model, messages, emit, deviceID, 2, 0)
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(answer) == "" {
		return emit("我现在回答不了，请稍后再试。")
	}
	v.remember(deviceID, text, answer)
	logger.Infof("voice answer done for %s: modelMS=%d chars=%d tool=%v", deviceID, time.Since(started).Milliseconds(), utf8.RuneCountInString(answer), len(called) > 0)
	return nil
}

func voiceToolOffered(tools []*schema.ToolInfo, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func (v *voiceConversation) stream(ctx context.Context, chat model.ToolCallingChatModel, messages []*schema.Message, emit func(string) error, deviceID string, request int, toolCount int) (answer string, calls []schema.ToolCall, err error) {
	started := time.Now()
	firstTokenMS := int64(-1)
	logger.Infof("voice model.start device=%s request=%d model=%s messages=%d tools=%d", deviceID, request, v.modelID, len(messages), toolCount)
	defer func() {
		if err != nil {
			logger.Infof("voice model.error device=%s request=%d model=%s elapsedMS=%d firstTokenMS=%d err=%v", deviceID, request, v.modelID, time.Since(started).Milliseconds(), firstTokenMS, err)
			return
		}
		logger.Infof("voice model.end device=%s request=%d model=%s elapsedMS=%d firstTokenMS=%d chars=%d toolCalls=%d", deviceID, request, v.modelID, time.Since(started).Milliseconds(), firstTokenMS, utf8.RuneCountInString(answer), len(calls))
	}()
	reader, err := chat.Stream(ctx, messages)
	if err != nil {
		return "", nil, err
	}
	defer reader.Close()
	var chunks []*schema.Message
	var buffer, complete strings.Builder
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return complete.String(), nil, recvErr
		}
		if chunk == nil {
			continue
		}
		if firstTokenMS < 0 && (chunk.Content != "" || len(chunk.ToolCalls) > 0) {
			firstTokenMS = time.Since(started).Milliseconds()
		}
		chunks = append(chunks, chunk)
		buffer.WriteString(chunk.Content)
		for {
			sentence, rest := voiceSentence(buffer.String(), false)
			if sentence == "" {
				break
			}
			if err := emit(sentence); err != nil {
				return complete.String(), nil, err
			}
			complete.WriteString(sentence)
			buffer.Reset()
			buffer.WriteString(rest)
		}
	}
	if len(chunks) == 0 {
		return "", nil, fmt.Errorf("voice model returned no output")
	}
	merged, err := schema.ConcatMessages(chunks)
	if err != nil {
		return "", nil, err
	}
	if len(merged.ToolCalls) == 0 {
		if sentence, _ := voiceSentence(buffer.String(), true); sentence != "" {
			if err := emit(sentence); err != nil {
				return complete.String(), nil, err
			}
			complete.WriteString(sentence)
		}
	}
	return strings.TrimSpace(merged.Content), merged.ToolCalls, nil
}

func voiceSentence(buffer string, final bool) (sentence, remainder string) {
	runes := utf8.RuneCountInString(buffer)
	for i, r := range buffer {
		if strings.ContainsRune("。！？!?；;\n", r) {
			end := i + utf8.RuneLen(r)
			return strings.TrimSpace(buffer[:end]), buffer[end:]
		}
		if strings.ContainsRune("，,", r) && utf8.RuneCountInString(buffer[:i]) >= 12 {
			end := i + utf8.RuneLen(r)
			return strings.TrimSpace(buffer[:end]), buffer[end:]
		}
	}
	if runes >= 40 {
		for i := range buffer {
			if utf8.RuneCountInString(buffer[:i]) >= 32 {
				return strings.TrimSpace(buffer[:i]), buffer[i:]
			}
		}
	}
	if final {
		return strings.TrimSpace(buffer), ""
	}
	return "", buffer
}
