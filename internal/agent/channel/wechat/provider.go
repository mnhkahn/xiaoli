package wechat

import (
	"context"

	agentchannel "github.com/mnhkahn/xiaoli/internal/agent/channel"
)

type ProviderConfig struct {
	Enabled bool
	Token   string
}

func Provider(cfg ProviderConfig) agentchannel.Provider {
	return provider{cfg: cfg}
}

type provider struct {
	cfg ProviderConfig
}

func (p provider) ListChannels(context.Context) ([]agentchannel.Info, error) {
	if !p.cfg.Enabled || p.cfg.Token == "" {
		return nil, nil
	}
	return []agentchannel.Info{
		{
			ID:             "wechat:bot",
			Type:           agentchannel.TypeWechat,
			DisplayName:    "微信",
			Status:         "configured",
			ConversationID: "wechat:bot",
			Capabilities: agentchannel.Capabilities{
				Text: true,
			},
		},
	}, nil
}
