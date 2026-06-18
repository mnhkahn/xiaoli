package lark

import (
	"context"

	agentchannel "xiaoli/server/internal/agent/channel"
)

type ProviderConfig struct {
	AppID   string
	Enabled bool
}

func Provider(cfg ProviderConfig) agentchannel.Provider {
	return provider{cfg: cfg}
}

type provider struct {
	cfg ProviderConfig
}

func (p provider) ListChannels(context.Context) ([]agentchannel.Info, error) {
	if !p.cfg.Enabled {
		return nil, nil
	}
	return []agentchannel.Info{
		{
			ID:             "lark:app:" + p.cfg.AppID,
			Type:           agentchannel.TypeLark,
			DisplayName:    "飞书",
			Status:         "configured",
			ConversationID: "lark:app:" + p.cfg.AppID,
			Capabilities: agentchannel.Capabilities{
				Text: true,
			},
		},
	}, nil
}
