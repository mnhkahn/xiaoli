package admin

import (
	"context"
	"errors"
	"fmt"
	"github.com/mnhkahn/gogogo/logger"
	"strings"
	"time"

	agentworkflow "xiaoli/server/internal/agent/workflow"
)

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
	}
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
	return c.agent.ChatWithContext(ctx, turn.ConversationID, turn.DeviceID, turn.Text)
}

func (c einoConversationChat) ChatWithOptions(ctx context.Context, turn ConversationTurn, opts ChatOptions) (string, error) {
	return c.agent.ChatWithContextOptions(ctx, turn.ConversationID, turn.DeviceID, turn.Text, opts)
}
