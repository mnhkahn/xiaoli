package runtime

import "time"

type Config struct {
	LLMURL                  string
	LLMAPIKey               string
	LLMModel                string
	LLMModels               []string
	LLMModelConfigs         map[string]LLMModelConfig
	LLMPrompt               string
	LLMTimeout              time.Duration
	VLLMModel               string
	ASRModel                string
	TTSModel                string
	RedisURL                string
	RedisKeyPrefix          string
	MemoryTTL               time.Duration
	ExternalMCPURLs         []string
	BuiltinWebFetchEnabled  bool
	SkillRoots              []string
	EnabledSkills           []string
	SkillMaxBytes           int64
	SkillExecTimeout        time.Duration
	SkillExecMaxOutputBytes int64
	SkillExecGlobalBinDirs  []string
}

type LLMModelConfig struct {
	ID            string
	DisplayName   string
	BaseURL       string
	Model         string
	APIKey        string
	MaxTokens     int
	ContextLength int
}

func (c Config) selectedLLMModelConfig() LLMModelConfig {
	return c.selectedLLMModelConfigFor(c.LLMModel)
}

func (c Config) selectedLLMModelConfigFor(id string) LLMModelConfig {
	if c.LLMModelConfigs != nil {
		if model, ok := c.LLMModelConfigs[id]; ok {
			if model.ID == "" {
				model.ID = id
			}
			return model
		}
	}
	return LLMModelConfig{
		ID:      id,
		BaseURL: c.LLMURL,
		Model:   id,
		APIKey:  c.LLMAPIKey,
	}
}
