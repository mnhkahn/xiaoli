package admin

import "testing"

func TestFirstOpenRouterFreeModelUsesCatalogOrder(t *testing.T) {
	models := []string{
		"vendor/paid-model",
		"first/free-model:free",
		"second/free-model:free",
	}
	if got := firstOpenRouterFreeModel(models); got != "first/free-model:free" {
		t.Fatalf("first OpenRouter free model = %q, want first/free-model:free", got)
	}
}

func TestOpenRouterModelIDUsesConfiguredAlias(t *testing.T) {
	configs := map[string]LLMModelConfig{
		"openrouter:minimax-m3-free": {Model: "minimax/minimax-m3:free"},
	}
	if got := openRouterModelID(configs, "minimax/minimax-m3:free"); got != "openrouter:minimax-m3-free" {
		t.Fatalf("model ID = %q, want configured alias", got)
	}
	if got := openRouterModelID(configs, "vendor/new:free"); got != "openrouter:vendor/new:free" {
		t.Fatalf("model ID = %q, want generated ID", got)
	}
}
