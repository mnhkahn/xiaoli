package runtime

import "time"

type Config struct {
	LLMURL                  string
	LLMAPIKey               string
	LLMModel                string
	LLMModels               []string
	LLMPrompt               string
	LLMTimeout              time.Duration
	VLLMModel               string
	ASRModel                string
	TTSModel                string
	RedisURL                string
	RedisKeyPrefix          string
	MemoryTTL               time.Duration
	ExternalMCPURLs         []string
	SkillRoots              []string
	EnabledSkills           []string
	SkillMaxBytes           int64
	SkillExecTimeout        time.Duration
	SkillExecMaxOutputBytes int64
	SkillExecGlobalBinDirs  []string
}
