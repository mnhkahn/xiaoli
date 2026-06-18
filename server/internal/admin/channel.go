package admin

import (
	"context"

	agentchannel "xiaoli/server/internal/agent/channel"
	agentlark "xiaoli/server/internal/agent/channel/lark"
	agentwechat "xiaoli/server/internal/agent/channel/wechat"
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
