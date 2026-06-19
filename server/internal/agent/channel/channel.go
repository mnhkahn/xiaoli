package channel

import (
	"context"
	"sort"
)

type ChannelFormatter interface {
	Instruction() string
	Send(ctx context.Context, reply string) error
}

type Type string

const (
	TypeESP32  Type = "esp32"
	TypeLark   Type = "lark"
	TypeWechat Type = "wechat"
)

type Capabilities struct {
	Text  bool `json:"text"`
	Image bool `json:"image"`
	Audio bool `json:"audio"`
	Tools bool `json:"tools"`
	Video bool `json:"video"`
}

type Info struct {
	ID             string         `json:"id"`
	Type           Type           `json:"type"`
	DisplayName    string         `json:"display_name"`
	Status         string         `json:"status"`
	ConversationID string         `json:"conversation_id,omitempty"`
	DeviceID       string         `json:"device_id,omitempty"`
	Capabilities   Capabilities   `json:"capabilities"`
	LastActivity   float64        `json:"last_activity,omitempty"`
	Raw            map[string]any `json:"raw,omitempty"`
}

type Provider interface {
	ListChannels(ctx context.Context) ([]Info, error)
}

type Registry struct {
	providers []Provider
}

func NewRegistry(providers ...Provider) Registry {
	return Registry{providers: providers}
}

func (r Registry) List(ctx context.Context) ([]Info, error) {
	var channels []Info
	for _, provider := range r.providers {
		if provider == nil {
			continue
		}
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
		return typeRank(channels[i].Type) < typeRank(channels[j].Type)
	})
	return channels, nil
}

type DeviceInfo struct {
	DeviceID     string
	SessionID    string
	ClientIP     string
	MCPReady     bool
	ToolCount    int
	ConnectedAt  float64
	LastActivity float64
	Raw          any
}

func ESP32InfoFromDevice(device DeviceInfo) Info {
	displayName := device.DeviceID
	if displayName == "" {
		displayName = "ESP32"
	}
	raw := map[string]any{}
	if device.Raw != nil {
		raw["device"] = device.Raw
	}
	return Info{
		ID:             "esp32:" + device.DeviceID,
		Type:           TypeESP32,
		DisplayName:    displayName,
		Status:         "online",
		ConversationID: device.DeviceID,
		DeviceID:       device.DeviceID,
		LastActivity:   device.LastActivity,
		Capabilities: Capabilities{
			Text:  true,
			Image: true,
			Audio: true,
			Tools: device.MCPReady,
			Video: true,
		},
		Raw: raw,
	}
}

func StaticInfo(info Info) Provider {
	return staticInfoProvider{info: info}
}

type staticInfoProvider struct {
	info Info
}

func (p staticInfoProvider) ListChannels(context.Context) ([]Info, error) {
	return []Info{p.info}, nil
}

func typeRank(typ Type) int {
	switch typ {
	case TypeESP32:
		return 0
	case TypeLark:
		return 1
	case TypeWechat:
		return 2
	default:
		return 99
	}
}
