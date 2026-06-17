package admin

import (
	"context"

	agentchannel "xiaoli/server/internal/agent/channel"
)

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
		larkChannelProvider{cfg: s.cfg},
		wechatChannelProvider{cfg: s.cfg},
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

type larkChannelProvider struct {
	cfg Config
}

func (p larkChannelProvider) ListChannels(ctx context.Context) ([]ChannelInfo, error) {
	if !p.cfg.LarkEnabled() {
		return nil, nil
	}
	return []ChannelInfo{
		{
			ID:             "lark:app:" + p.cfg.LarkAppID,
			Type:           ChannelTypeLark,
			DisplayName:    "飞书",
			Status:         "configured",
			ConversationID: "lark:app:" + p.cfg.LarkAppID,
			Capabilities: ChannelCapabilities{
				Text: true,
			},
		},
	}, nil
}

type wechatChannelProvider struct {
	cfg Config
}

func (p wechatChannelProvider) ListChannels(ctx context.Context) ([]ChannelInfo, error) {
	if !p.cfg.WeChatEnabled || p.cfg.WeChatBotToken == "" {
		return nil, nil
	}
	return []ChannelInfo{
		{
			ID:             "wechat:bot",
			Type:           ChannelTypeWechat,
			DisplayName:    "微信",
			Status:         "configured",
			ConversationID: "wechat:bot",
			Capabilities: ChannelCapabilities{
				Text: true,
			},
		},
	}, nil
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
