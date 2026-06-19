package admin

import (
	agentmedia "xiaoli/server/internal/agent/media"
	agentruntime "xiaoli/server/internal/agent/runtime"
)

type SpeechRecognizer = agentmedia.SpeechRecognizer
type VisionAnalyzer = agentmedia.VisionAnalyzer
type EinoAgent = agentruntime.Agent
type ChatOptions = agentruntime.ChatOptions
type memoryReader = agentruntime.MemoryReader
type memoryKeyInfo = agentruntime.MemoryKeyInfo
type memoryValue = agentruntime.MemoryValue

func newOpenAITranscriber(cfg Config) SpeechRecognizer {
	return agentmedia.NewOpenAITranscriber(agentmedia.ASRConfig{
		URL:     cfg.GoASRURL,
		APIKey:  cfg.GoASRAPIKey,
		Model:   cfg.GoASRModel,
		Timeout: cfg.GoASRTimeout,
	})
}

func newGoVisionClient(cfg Config) VisionAnalyzer {
	return agentmedia.NewOpenAIVisionClient(agentmedia.VisionConfig{
		URL:     cfg.GoVLLMURL,
		APIKey:  cfg.GoVLLMAPIKey,
		Model:   cfg.GoVLLMModel,
		Timeout: cfg.GoVLLMTimeout,
	})
}

func newEinoAgent(cfg Config) *EinoAgent {
	return agentruntime.NewAgent(agentruntime.Config{
		LLMURL:                  cfg.GoLLMURL,
		LLMAPIKey:               cfg.GoLLMAPIKey,
		LLMModel:                cfg.GoLLMModel,
		LLMModels:               cfg.GoLLMModels,
		LLMPrompt:               cfg.GoLLMPrompt,
		LLMTimeout:              cfg.GoLLMTimeout,
		VLLMModel:               cfg.GoVLLMModel,
		ASRModel:                cfg.GoASRModel,
		TTSModel:                cfg.GoTTSModel,
		RedisURL:                cfg.RedisURL,
		RedisKeyPrefix:          cfg.RedisKeyPrefix,
		MemoryTTL:               cfg.MemoryTTL,
		ExternalMCPURLs:         cfg.ExternalMCPURLs,
		SkillRoots:              cfg.SkillRoots,
		EnabledSkills:           cfg.EnabledSkills,
		SkillMaxBytes:           cfg.SkillMaxBytes,
		SkillExecTimeout:        cfg.SkillExecTimeout,
		SkillExecMaxOutputBytes: cfg.SkillExecMaxOutputBytes,
		SkillExecGlobalBinDirs:  cfg.SkillExecGlobalBinDirs,
	})
}

func newRedisMemory(cfg Config) memoryReader {
	return agentruntime.NewRedisMemory(agentruntime.Config{
		RedisURL:       cfg.RedisURL,
		RedisKeyPrefix: cfg.RedisKeyPrefix,
		MemoryTTL:      cfg.MemoryTTL,
	})
}
