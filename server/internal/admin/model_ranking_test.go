package admin

import "testing"

func TestSelectTopOpenRouterFreeModelUsesCodeRankThenELO(t *testing.T) {
	models := []rankedModel{
		{Platform: "OpenRouter", Model: "unrated/model:free", CodeRank: 0, CodeELO: 2000},
		{Platform: "OpenRouter", Model: "paid/model", CodeRank: 1, CodeELO: 2000},
		{Platform: "OpenRouter", Model: "text-better:free", CodeRank: 40, CodeELO: 1500, TextRank: 1, TextELO: 1600},
		{Platform: "OpenRouter", Model: "code-best-low-elo:free", CodeRank: 37, CodeELO: 1400, TextRank: 100, TextELO: 1400},
		{Platform: "OpenRouter", Model: "code-best-high-elo:free", CodeRank: 37, CodeELO: 1500, TextRank: 200, TextELO: 1300},
		{Platform: "SiliconFlow", Model: "other-provider:free", CodeRank: 1, CodeELO: 3000},
	}

	got, ok := selectTopOpenRouterFreeModel(models)
	if !ok {
		t.Fatal("selectTopOpenRouterFreeModel() found no model")
	}
	if got.Model != "code-best-high-elo:free" {
		t.Fatalf("selected model = %q, want code-best-high-elo:free", got.Model)
	}
}

func TestRankedOpenRouterModelIDUsesConfiguredAlias(t *testing.T) {
	configs := map[string]LLMModelConfig{
		"openrouter:minimax-m3-free": {Model: "minimax/minimax-m3:free"},
	}
	if got := rankedOpenRouterModelID(configs, "minimax/minimax-m3:free"); got != "openrouter:minimax-m3-free" {
		t.Fatalf("model ID = %q, want configured alias", got)
	}
	if got := rankedOpenRouterModelID(configs, "vendor/new:free"); got != "openrouter:vendor/new:free" {
		t.Fatalf("model ID = %q, want generated ID", got)
	}
}
