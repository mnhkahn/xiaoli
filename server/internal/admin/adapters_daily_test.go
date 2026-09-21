package admin

import (
	"context"
	"errors"
	"testing"
)

func TestGenerateDailyEncouragementRetriesInvalidResponses(t *testing.T) {
	attempts := 0
	got, err := generateDailyEncouragement(context.Background(), "system", "user", func(context.Context, string, string) (string, error) {
		attempts++
		switch attempts {
		case 1:
			return `<dots_function_call>{"command":"curl"}`, nil
		case 2:
			return "", errors.New("model returned empty response")
		default:
			return "今天是周一，把最重要的一件事做好，新的一周就从这一步开始。", nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if got != "今天是周一，把最重要的一件事做好，新的一周就从这一步开始。" {
		t.Fatalf("greeting = %q", got)
	}
}

func TestGenerateDailyEncouragementStopsAfterThreeFailures(t *testing.T) {
	attempts := 0
	_, err := generateDailyEncouragement(context.Background(), "system", "user", func(context.Context, string, string) (string, error) {
		attempts++
		return `{ "command": "curl" }`, nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != dailyEncouragementMaxAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, dailyEncouragementMaxAttempts)
	}
}
