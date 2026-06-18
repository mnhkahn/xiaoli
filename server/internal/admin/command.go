package admin

import (
	"context"

	agentchannel "xiaoli/server/internal/agent/channel"
	"xiaoli/server/internal/agent/slash"
	agentskill "xiaoli/server/internal/agent/tool/skill"
)

type builtinCommand = slash.Command

func parseBuiltinCommand(text string) (builtinCommand, bool) {
	cmd, ok := slash.Parse(text)
	if !ok {
		return builtinCommand{}, false
	}
	switch cmd.Name {
	case "skills", "model", "channel":
		return cmd, true
	default:
		return builtinCommand{}, false
	}
}

func (s *AdminServer) handleBuiltinCommand(ctx context.Context, source ConversationChannel, text string) (string, bool) {
	sourceType, ok := slashSourceType(source)
	if !ok {
		return "", false
	}
	return slash.NewHandler(adminSlashDeps{s: s}).Handle(ctx, sourceType, text)
}

func slashSourceType(source ConversationChannel) (agentchannel.Type, bool) {
	switch source {
	case ChannelLarkText:
		return agentchannel.TypeLark, true
	case ChannelWechatText:
		return agentchannel.TypeWechat, true
	case ChannelDeviceVoice:
		return agentchannel.TypeESP32, true
	default:
		return "", false
	}
}

type adminSlashDeps struct {
	s *AdminServer
}

func (d adminSlashDeps) ListSkills(ctx context.Context) ([]slash.SkillInfo, error) {
	if len(d.s.cfg.SkillRoots) == 0 {
		return nil, nil
	}
	backend, err := agentskill.NewFileBackend(agentskill.BackendConfig{
		Roots:    d.s.cfg.SkillRoots,
		Enabled:  d.s.cfg.EnabledSkills,
		MaxBytes: d.s.cfg.SkillMaxBytes,
	})
	if err != nil {
		return nil, err
	}
	skills, err := backend.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]slash.SkillInfo, 0, len(skills))
	for _, skill := range skills {
		out = append(out, slash.SkillInfo{Name: skill.Name, Description: skill.Description})
	}
	return out, nil
}

func (d adminSlashDeps) ModelInfo() slash.ModelInfo {
	return slash.ModelInfo{
		LLM:  d.s.cfg.GoLLMModel,
		VLLM: d.s.cfg.GoVLLMModel,
		ASR:  d.s.cfg.GoASRModel,
		TTS:  d.s.cfg.GoTTSModel,
	}
}

func (d adminSlashDeps) ListChannels(ctx context.Context) ([]agentchannel.Info, error) {
	return d.s.channels(ctx)
}
