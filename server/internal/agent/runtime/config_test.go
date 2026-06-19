package runtime

import "testing"

func TestSelectedLLMModelConfigUsesConfiguredModelDetails(t *testing.T) {
	cfg := Config{
		LLMModel: "siliconflow:qwen3-8b",
		LLMModelConfigs: map[string]LLMModelConfig{
			"siliconflow:qwen3-8b": {
				BaseURL: "https://api.example.test/v1/chat/completions",
				Model:   "Qwen/Qwen3-8B",
				APIKey:  "secret",
			},
		},
	}

	model := cfg.selectedLLMModelConfig()

	if model.ID != "siliconflow:qwen3-8b" {
		t.Fatalf("ID = %q, want selected id", model.ID)
	}
	if model.BaseURL != "https://api.example.test/v1/chat/completions" || model.Model != "Qwen/Qwen3-8B" || model.APIKey != "secret" {
		t.Fatalf("model = %#v, want configured endpoint", model)
	}
}

