package model

import "testing"

func TestSelectorUsesConfiguredModel(t *testing.T) {
	selector := NewSelector(
		map[Role]string{RoleLLM: "model-a"},
		map[Role][]Option{RoleLLM: OptionsFromIDs(RoleLLM, []string{"model-a", "model-b"})},
	)

	if got := selector.Current(RoleLLM); got != "model-a" {
		t.Fatalf("Current() = %q, want model-a", got)
	}
	if err := selector.Use(RoleLLM, "model-b"); err != nil {
		t.Fatalf("Use() error = %v", err)
	}
	if got := selector.Current(RoleLLM); got != "model-b" {
		t.Fatalf("Current() = %q, want model-b", got)
	}
}

func TestSelectorRejectsUnconfiguredModel(t *testing.T) {
	selector := NewSelector(
		map[Role]string{RoleLLM: "model-a"},
		map[Role][]Option{RoleLLM: OptionsFromIDs(RoleLLM, []string{"model-a"})},
	)

	if err := selector.Use(RoleLLM, "model-b"); err == nil {
		t.Fatal("Use() error = nil, want unconfigured model error")
	}
}

func TestSelectorAddsDefaultToOptions(t *testing.T) {
	selector := NewSelector(map[Role]string{RoleLLM: "model-a"}, nil)

	items := selector.List(RoleLLM)
	if len(items) != 1 || items[0].ID != "model-a" {
		t.Fatalf("List() = %#v, want default model as option", items)
	}
}
