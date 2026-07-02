package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestLocalMemorySaveLoadAndList(t *testing.T) {
	dir := t.TempDir()
	mem := NewLocalMemory(Config{
		LocalDataDir:            dir,
		LocalConversationDir:    "conversations",
		LocalHistoryMaxMessages: 2,
	})

	msgs := []*schema.Message{
		schema.UserMessage("one"),
		schema.AssistantMessage("two", nil),
		schema.UserMessage("three"),
	}
	if err := mem.Save(context.Background(), "ses/test:1", msgs); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got := mem.Load(context.Background(), "ses/test:1")
	if len(got) != 2 {
		t.Fatalf("Load() messages = %d, want 2", len(got))
	}
	if got[0].Content != "two" || got[1].Content != "three" {
		t.Fatalf("Load() = [%q, %q], want [two, three]", got[0].Content, got[1].Content)
	}
	if _, err := os.Stat(filepath.Join(dir, "conversations", "ses_test_1.json")); err != nil {
		t.Fatalf("conversation file not written: %v", err)
	}

	items, err := mem.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].TTLSeconds != -1 {
		t.Fatalf("List() = %#v, want one non-expiring item", items)
	}
}

func TestLocalMemoryBackendsUseMemoryMD(t *testing.T) {
	dir := t.TempDir()
	mem := NewLocalMemory(Config{LocalDataDir: dir, LocalMemoryFile: "Memory.md"})
	backends := mem.MemoryBackends("tui", "local")
	if backends == nil || backends.Global == nil || backends.Channel == nil {
		t.Fatal("MemoryBackends() missing local backends")
	}
	if err := backends.Global.Save(context.Background(), "language", "中文"); err != nil {
		t.Fatalf("global Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Memory.md")); err != nil {
		t.Fatalf("Memory.md not written: %v", err)
	}
	data, err := backends.Global.List(context.Background())
	if err != nil {
		t.Fatalf("global List() error = %v", err)
	}
	if data["language"] != "中文" {
		t.Fatalf("global memory = %#v, want language", data)
	}
}
