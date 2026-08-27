package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestLocalTaskContractForErrorStack(t *testing.T) {
	contract, ok := localTaskContract("查看 New Relic 的错误堆栈")
	if !ok {
		t.Fatal("expected stack request to use local contract template")
	}
	if contract.Source != "template" || len(contract.Requirements) != 2 {
		t.Fatalf("contract = %#v", contract)
	}
}

func TestTaskContractRequiresRealToolEvidence(t *testing.T) {
	state := TaskContractState{Contract: TaskContract{Requirements: []CapabilityRequirement{
		{Capability: "observability.error_stack", Required: true},
		{Capability: "observability.error_context", Required: true},
	}}}
	state.mergeEvidence([]ToolEvidence{{Tool: "newrelic.query", Capabilities: []string{"observability.error_count"}}})
	missing := state.missing()
	if len(missing) != 2 {
		t.Fatalf("missing = %v, want both contract capabilities", missing)
	}
	state.mergeEvidence([]ToolEvidence{{Tool: "newrelic.query", Capabilities: []string{"observability.error_stack", "observability.error_context"}}})
	if missing := state.missing(); len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
}

func TestCapabilitiesFromToolOutput(t *testing.T) {
	capabilities := capabilitiesFromToolOutput("newrelic.query", "{}", `{"errorStack":"at x","errorMessage":"boom","pageUrl":"/checkout"}`)
	joined := strings.Join(capabilities, ",")
	for _, want := range []string{"observability.error_stack", "observability.error_context"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("capabilities = %v, missing %s", capabilities, want)
		}
	}
}

func TestUnavailableFieldsAreVerifiedEvidence(t *testing.T) {
	state := TaskContractState{Contract: TaskContract{Requirements: []CapabilityRequirement{
		{Capability: "observability.error_stack", Required: true},
		{Capability: "observability.error_context", Required: true},
	}}}
	capabilities := capabilitiesFromToolOutput("newrelic.query", "{}", "errorStack, errorMessage, and pageUrl are unavailable")
	state.mergeEvidence([]ToolEvidence{{Tool: "newrelic.query", Capabilities: capabilities}})
	if missing := state.missing(); len(missing) != 0 {
		t.Fatalf("missing = %v, want unavailable fields to verify the contract", missing)
	}
}

func TestTaskContractStatePersistsLocally(t *testing.T) {
	memory := NewLocalMemory(Config{LocalDataDir: t.TempDir()})
	want := TaskContractState{Contract: conservativeTaskContract("开放任务"), Status: "active"}
	if err := memory.SaveTaskContractState(context.Background(), "session-1", want); err != nil {
		t.Fatalf("SaveTaskContractState() error = %v", err)
	}
	got, ok := memory.LoadTaskContractState(context.Background(), "session-1")
	if !ok || got.Contract.Source != "fallback" || got.Status != "active" {
		t.Fatalf("LoadTaskContractState() = %#v, %v", got, ok)
	}
}

func TestTaskContractStopMessage(t *testing.T) {
	got := taskContractStopMessage("已经完成", []string{"observability.error_stack"})
	if !strings.Contains(got, "未验证停止") || !strings.Contains(got, "observability.error_stack") {
		t.Fatalf("stop message = %q", got)
	}
}
