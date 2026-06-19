package session

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func testManager(t *testing.T) (*Manager, context.Context) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	m := NewManager(client, "t:")
	return m, context.Background()
}

func TestCreateAndGet(t *testing.T) {
	m, ctx := testManager(t)

	sid, isNew, err := m.Create(ctx, "user1", "model-a")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if !isNew {
		t.Fatal("expected isNew=true")
	}
	if len(sid) < 10 || sid[:4] != "ses_" {
		t.Fatalf("unexpected session id: %q", sid)
	}

	info, err := m.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if info.Title != "新会话" || info.Model != "model-a" || info.UserID != "user1" {
		t.Fatalf("unexpected info: %+v", info)
	}

	_, err = m.Get(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestGetOrCreateReusesActive(t *testing.T) {
	m, ctx := testManager(t)

	sid1, _, _ := m.Create(ctx, "user2", "model-b")

	sid2, isNew, err := m.GetOrCreate(ctx, "user2", "model-c")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if isNew {
		t.Fatal("expected isNew=false (reuse existing)")
	}
	if sid1 != sid2 {
		t.Fatalf("sid mismatch: %q vs %q", sid1, sid2)
	}

	info, _ := m.Get(ctx, sid2)
	if info.Model != "model-b" {
		t.Fatalf("model should stay model-b, got %q", info.Model)
	}
}

func TestGetOrCreateCreatesNew(t *testing.T) {
	m, ctx := testManager(t)

	sid, isNew, err := m.GetOrCreate(ctx, "user3", "model-d")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if !isNew {
		t.Fatal("expected isNew=true")
	}
	info, _ := m.Get(ctx, sid)
	if info.UserID != "user3" {
		t.Fatalf("user_id = %q, want user3", info.UserID)
	}
}

func TestListFiltersByUser(t *testing.T) {
	m, ctx := testManager(t)

	m.Create(ctx, "user_a", "m1")
	m.Create(ctx, "user_a", "m2")
	m.Create(ctx, "user_b", "m3")

	sessions, err := m.List(ctx, "user_a")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("user_a should have 2 sessions, got %d", len(sessions))
	}

	sessions, err = m.List(ctx, "user_b")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("user_b should have 1 session, got %d", len(sessions))
	}

	sessions, err = m.List(ctx, "user_c")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("user_c should have 0 sessions, got %d", len(sessions))
	}
}

func TestSetTitle(t *testing.T) {
	m, ctx := testManager(t)

	sid, _, _ := m.Create(ctx, "user4", "m4")
	m.SetTitle(ctx, sid, "我的第一个会话")

	info, _ := m.Get(ctx, sid)
	if info.Title != "我的第一个会话" {
		t.Fatalf("title = %q, want 我的第一个会话", info.Title)
	}
}

func TestUpdateAfterChat(t *testing.T) {
	m, ctx := testManager(t)

	sid, _, _ := m.Create(ctx, "user5", "m5")
	m.UpdateAfterChat(ctx, sid, 3)

	info, _ := m.Get(ctx, sid)
	if info.Count != 3 {
		t.Fatalf("count = %d, want 3", info.Count)
	}
}

func TestOwnerIsolation(t *testing.T) {
	m, ctx := testManager(t)

	m.Create(ctx, "user6", "m6")

	sessions, _ := m.List(ctx, "user7")
	for _, s := range sessions {
		if s.UserID == "user7" {
			t.Fatal("user7 should not see user6's sessions")
		}
	}
}

func TestCheckEpochInitializedOnFirstCall(t *testing.T) {
	m, ctx := testManager(t)
	sid, _, _ := m.Create(ctx, "u1", "m1")

	r := m.ReconcileEpoch(ctx, sid, "2026-06-19")
	if r != EpochInitialized {
		t.Fatalf("first call should be Initialized, got %v", r)
	}
}

func TestCheckEpochSameDay(t *testing.T) {
	m, ctx := testManager(t)
	sid, _, _ := m.Create(ctx, "u1", "m1")

	m.CommitEpoch(ctx, sid, "2026-06-19")
	r := m.ReconcileEpoch(ctx, sid, "2026-06-19")
	if r != EpochUnchanged {
		t.Fatalf("same day should be Unchanged, got %v", r)
	}
}

func TestCheckEpochCrossDay(t *testing.T) {
	m, ctx := testManager(t)
	sid, _, _ := m.Create(ctx, "u1", "m1")

	m.CommitEpoch(ctx, sid, "2026-06-19")
	r := m.ReconcileEpoch(ctx, sid, "2026-06-20")
	if r != EpochUpdated {
		t.Fatalf("cross day should be Updated, got %v", r)
	}
}