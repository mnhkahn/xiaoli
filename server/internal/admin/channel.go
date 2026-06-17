package admin

import (
	"context"
	"sort"
)

type ChannelType string

const (
	ChannelTypeESP32  ChannelType = "esp32"
	ChannelTypeLark   ChannelType = "lark"
	ChannelTypeWechat ChannelType = "wechat"
)

type ChannelCapabilities struct {
	Text  bool `json:"text"`
	Image bool `json:"image"`
	Audio bool `json:"audio"`
	Tools bool `json:"tools"`
	Video bool `json:"video"`
}

type ChannelInfo struct {
	ID             string              `json:"id"`
	Type           ChannelType         `json:"type"`
	DisplayName    string              `json:"display_name"`
	Status         string              `json:"status"`
	ConversationID string              `json:"conversation_id,omitempty"`
	DeviceID       string              `json:"device_id,omitempty"`
	Capabilities   ChannelCapabilities `json:"capabilities"`
	LastActivity   float64             `json:"last_activity,omitempty"`
	Raw            map[string]any      `json:"raw,omitempty"`
}

type ChannelProvider interface {
	ListChannels(ctx context.Context) ([]ChannelInfo, error)
}

func (s *AdminServer) channels(ctx context.Context) ([]ChannelInfo, error) {
	var channels []ChannelInfo
	for _, provider := range s.channelProviders() {
		list, err := provider.ListChannels(ctx)
		if err != nil {
			return nil, err
		}
		channels = append(channels, list...)
	}
	sort.SliceStable(channels, func(i, j int) bool {
		if channels[i].Type == channels[j].Type {
			return channels[i].ID < channels[j].ID
		}
		return channelTypeRank(channels[i].Type) < channelTypeRank(channels[j].Type)
	})
	return channels, nil
}

func (s *AdminServer) channelProviders() []ChannelProvider {
	return []ChannelProvider{
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
	displayName := device.DeviceID
	if displayName == "" {
		displayName = "ESP32"
	}
	return ChannelInfo{
		ID:             "esp32:" + device.DeviceID,
		Type:           ChannelTypeESP32,
		DisplayName:    displayName,
		Status:         "online",
		ConversationID: device.DeviceID,
		DeviceID:       device.DeviceID,
		LastActivity:   device.LastActivity,
		Capabilities: ChannelCapabilities{
			Text:  true,
			Image: true,
			Audio: true,
			Tools: device.MCPReady,
			Video: true,
		},
		Raw: map[string]any{
			"device": device,
		},
	}
}

func channelTypeRank(typ ChannelType) int {
	switch typ {
	case ChannelTypeESP32:
		return 0
	case ChannelTypeLark:
		return 1
	case ChannelTypeWechat:
		return 2
	default:
		return 99
	}
}
