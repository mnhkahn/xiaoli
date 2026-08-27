package builtin

import (
	"context"
	"strings"
	"testing"
)

func TestAskUserQuestionAcceptsFreeModelOptionShapes(t *testing.T) {
	tool := NewAskUserQuestionTool()
	for _, raw := range []string{
		`{"question":"继续吗？","options":"继续|停止"}`,
		`{"question":"继续吗？","options":["继续","停止"]}`,
		`{"question":"继续吗？","options":{"first":"继续","second":"停止"}}`,
	} {
		result, err := tool.InvokableRun(context.Background(), raw)
		if err != nil {
			t.Fatalf("InvokableRun(%s) error = %v", raw, err)
		}
		if !strings.Contains(result, "已向用户提问") {
			t.Fatalf("InvokableRun(%s) = %q", raw, result)
		}
	}
}
