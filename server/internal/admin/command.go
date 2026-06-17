package admin

import (
	"context"
	"fmt"
	"strings"
)

type builtinCommand struct {
	Name string
	Args string
}

func parseBuiltinCommand(text string) (builtinCommand, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return builtinCommand{}, false
	}
	fields := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(fields) == 0 {
		return builtinCommand{}, false
	}
	name := strings.ToLower(fields[0])
	switch name {
	case "skills", "model", "channel":
		return builtinCommand{Name: name, Args: strings.TrimSpace(strings.Join(fields[1:], " "))}, true
	default:
		return builtinCommand{}, false
	}
}

func (s *AdminServer) handleBuiltinCommand(ctx context.Context, channel ConversationChannel, text string) (string, bool) {
	if channel == ChannelDeviceVoice {
		return "", false
	}
	cmd, ok := parseBuiltinCommand(text)
	if !ok {
		return "", false
	}
	switch cmd.Name {
	case "skills":
		return s.builtinSkills(ctx), true
	case "model":
		return s.builtinModel(), true
	case "channel":
		return s.builtinChannel(ctx), true
	default:
		return "", false
	}
}

func (s *AdminServer) builtinSkills(ctx context.Context) string {
	if len(s.cfg.SkillRoots) == 0 {
		return "当前没有配置 Skill 根目录。"
	}
	backend, err := newFileSkillBackend(fileSkillBackendConfig{
		Roots:    s.cfg.SkillRoots,
		Enabled:  s.cfg.EnabledSkills,
		MaxBytes: s.cfg.SkillMaxBytes,
	})
	if err != nil {
		return "读取 Skill 列表失败：" + err.Error()
	}
	skills, err := backend.List(ctx)
	if err != nil {
		return "读取 Skill 列表失败：" + err.Error()
	}
	if len(skills) == 0 {
		return "当前没有启用的 Skill。"
	}
	var b strings.Builder
	b.WriteString("可用 Skills：")
	for _, skill := range skills {
		b.WriteString("\n- ")
		b.WriteString(skill.Name)
		if skill.Description != "" {
			b.WriteString("：")
			b.WriteString(skill.Description)
		}
	}
	return b.String()
}

func (s *AdminServer) builtinModel() string {
	var b strings.Builder
	b.WriteString("当前模型配置：")
	writeCommandValue(&b, "LLM", s.cfg.GoLLMModel)
	writeCommandValue(&b, "VLLM", s.cfg.GoVLLMModel)
	writeCommandValue(&b, "ASR", s.cfg.GoASRModel)
	writeCommandValue(&b, "TTS", s.cfg.GoTTSModel)
	return b.String()
}

func (s *AdminServer) builtinChannel(ctx context.Context) string {
	channels, err := s.channels(ctx)
	if err != nil {
		return "读取 Channel 列表失败：" + err.Error()
	}
	if len(channels) == 0 {
		return "当前没有可用 Channel。"
	}
	var b strings.Builder
	b.WriteString("可用 Channels：")
	for _, channel := range channels {
		b.WriteString("\n- ")
		b.WriteString(channel.ID)
		b.WriteString(" [")
		b.WriteString(string(channel.Type))
		b.WriteString("]")
		if channel.Status != "" {
			b.WriteString(" ")
			b.WriteString(channel.Status)
		}
		if channel.DeviceID != "" {
			b.WriteString(" device=")
			b.WriteString(channel.DeviceID)
		}
	}
	return b.String()
}

func writeCommandValue(b *strings.Builder, name, value string) {
	if strings.TrimSpace(value) == "" {
		value = "未配置"
	}
	fmt.Fprintf(b, "\n- %s: %s", name, value)
}
