package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestPromptProfileStructuredOutputCapturesNormalizedResult(t *testing.T) {
	output := NewPromptProfileStructuredOutput(
		"structured_output",
		"Return a structured result.",
		map[string]*schema.ParameterInfo{
			"answer": {Type: schema.String, Required: true},
		},
		func(value string) (string, error) {
			return strings.ToUpper(value), nil
		},
	)
	tool := newPromptProfileStructuredOutputTool(output)

	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Name != "structured_output" {
		t.Fatalf("Info().Name = %q, want structured_output", info.Name)
	}
	if _, err := tool.InvokableRun(context.Background(), "{\n  \"answer\": \"ok\"\n}"); err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	got, ok := output.Result()
	if !ok || got != `{"ANSWER":"OK"}` {
		t.Fatalf("Result() = %q, %v; want normalized compact JSON", got, ok)
	}
}

func TestPromptProfileStructuredOutputRejectsInvalidJSON(t *testing.T) {
	output := NewPromptProfileStructuredOutput("structured_output", "Return a structured result.", nil, nil)
	tool := newPromptProfileStructuredOutputTool(output)

	if _, err := tool.InvokableRun(context.Background(), `{"answer":`); err == nil {
		t.Fatal("InvokableRun() error = nil, want invalid JSON error")
	}
	if _, ok := output.Result(); ok {
		t.Fatal("Result() ok = true after invalid JSON")
	}
}
