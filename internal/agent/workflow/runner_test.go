package workflow

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAgent struct {
	failures int
	calls    int
}

func (a *fakeAgent) Run(_ context.Context, req AgentRequest) (AgentResponse, error) {
	a.calls++
	if req.Attempt != a.calls {
		return AgentResponse{}, errors.New("attempt mismatch")
	}
	if a.calls <= a.failures {
		return AgentResponse{}, errors.New("temporary failure")
	}
	return AgentResponse{Text: "ok", Finished: true}, nil
}

func TestRegistryListSortsDefinitions(t *testing.T) {
	registry, err := NewRegistry(
		Definition{ID: "b", Enabled: true},
		Definition{ID: "a", Enabled: true},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	got := registry.List()
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("List() = %#v, want sorted definitions", got)
	}
}

func TestRunnerRetriesUntilAgentSucceeds(t *testing.T) {
	registry, err := NewRegistry(Definition{
		ID:      "chat_react",
		Enabled: true,
		Agent:   AgentSpec{MaxSteps: 3, Timeout: time.Second},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	agent := &fakeAgent{failures: 2}
	runner := NewRunner(RunnerConfig{Registry: registry, Agent: agent})

	run, err := runner.Run(context.Background(), "chat_react", Input{Text: "hello"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if run.Status != RunSucceeded || run.Output.Text != "ok" {
		t.Fatalf("run = %#v, want success with output", run)
	}
	if len(run.Steps) != 3 || run.Steps[0].Status != StepFailed || run.Steps[2].Status != StepSucceeded {
		t.Fatalf("steps = %#v, want two failures then success", run.Steps)
	}
}

func TestRunnerStopsAtMaxSteps(t *testing.T) {
	registry, err := NewRegistry(Definition{
		ID:      "chat_react",
		Enabled: true,
		Agent:   AgentSpec{MaxSteps: 2, Timeout: time.Second},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	runner := NewRunner(RunnerConfig{Registry: registry, Agent: &fakeAgent{failures: 10}})

	run, err := runner.Run(context.Background(), "chat_react", Input{Text: "hello"})
	if err == nil {
		t.Fatal("Run() error = nil, want failure")
	}
	if run.Status != RunFailed || len(run.Steps) != 2 {
		t.Fatalf("run = %#v, want failed after two steps", run)
	}
}
