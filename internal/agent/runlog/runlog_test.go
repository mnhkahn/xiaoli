package runlog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentevent "github.com/mnhkahn/xiaoli-esp32/internal/event"
)

func TestWriterAppendsJSONLBySession(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir)
	event := agentevent.Event{
		Type:      agentevent.TypeAgentRunStarted,
		SessionID: "ses/test:1",
		Data:      map[string]any{"text": "hi"},
	}
	if err := w.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if err := w.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle() second error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ses_test_1.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Fatalf("line count = %d, want 2\n%s", got, data)
	}
}

func TestReadSessionReturnsRecentEvents(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir)
	for _, typ := range []string{
		agentevent.TypeAgentRunStarted,
		agentevent.TypeAgentToolStarted,
		agentevent.TypeAgentRunCompleted,
	} {
		if err := w.Handle(context.Background(), agentevent.Event{Type: typ, SessionID: "ses1"}); err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
	}
	events, err := ReadSession(dir, "ses1", 2)
	if err != nil {
		t.Fatalf("ReadSession() error = %v", err)
	}
	if len(events) != 2 || events[0].Type != agentevent.TypeAgentToolStarted || events[1].Type != agentevent.TypeAgentRunCompleted {
		t.Fatalf("events = %#v, want last two events", events)
	}
}
