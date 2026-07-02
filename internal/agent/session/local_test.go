package session

import (
	"context"
	"testing"
)

func TestLocalManagerPersistsSessionsAndChannels(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	m := NewLocalManager(dir)

	sid, created, err := m.GetOrCreate(ctx, "tui", "local", "model-a")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if !created || sid == "" {
		t.Fatalf("GetOrCreate() = %q, %v, want created session", sid, created)
	}
	m.SetTitle(ctx, sid, "hello local session")
	m.UpdateAfterChat(ctx, sid, 3)

	reloaded := NewLocalManager(dir)
	gotSID, created, err := reloaded.GetOrCreate(ctx, "tui", "local", "model-a")
	if err != nil {
		t.Fatalf("reloaded GetOrCreate() error = %v", err)
	}
	if created || gotSID != sid {
		t.Fatalf("reloaded session = %q created=%v, want %q false", gotSID, created, sid)
	}
	info, err := reloaded.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if info.Title != "hello local session" || info.Count != 3 {
		t.Fatalf("session info = %#v, want persisted title/count", info)
	}
}
