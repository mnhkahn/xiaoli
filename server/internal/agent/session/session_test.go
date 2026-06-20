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

	sid, isNew, err := m.Create(ctx, "lark", "user1", "model-a")
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
	if info.Title != "新会话" || info.Model != "model-a" || info.ChannelUser != "user1" || info.ChannelName != "lark" {
		t.Fatalf("unexpected info: %+v", info)
	}

	_, err = m.Get(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestGetOrCreateReusesExisting(t *testing.T) {
	m, ctx := testManager(t)

	sid1, _, _ := m.Create(ctx, "lark", "user2", "model-b")

	sid2, isNew, err := m.GetOrCreate(ctx, "lark", "user2", "model-c")
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

	sid, isNew, err := m.GetOrCreate(ctx, "wechat", "user3", "model-d")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if !isNew {
		t.Fatal("expected isNew=true")
	}
	info, _ := m.Get(ctx, sid)
	if info.ChannelName != "wechat" || info.ChannelUser != "user3" {
		t.Fatalf("channel mismatch: %+v", info)
	}
}

func TestListByChannel(t *testing.T) {
	m, ctx := testManager(t)

	m.Create(ctx, "lark", "user_a", "m1")
	m.Create(ctx, "lark", "user_a", "m2")
	m.Create(ctx, "lark", "user_b", "m3")

	sessions, err := m.ListByChannel(ctx, "lark", "user_a")
	if err != nil {
		t.Fatalf("ListByChannel failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("lark/user_a should have 2 sessions, got %d", len(sessions))
	}

	sessions, err = m.ListByChannel(ctx, "lark", "user_b")
	if err != nil {
		t.Fatalf("ListByChannel failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("lark/user_b should have 1 session, got %d", len(sessions))
	}

	sessions, err = m.ListByChannel(ctx, "lark", "user_c")
	if err != nil {
		t.Fatalf("ListByChannel failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("lark/user_c should have 0 sessions, got %d", len(sessions))
	}
}

func TestSetTitle(t *testing.T) {
	m, ctx := testManager(t)

	sid, _, _ := m.Create(ctx, "lark", "user4", "m4")
	m.SetTitle(ctx, sid, "我的第一个会话")

	info, _ := m.Get(ctx, sid)
	if info.Title != "我的第一个会话" {
		t.Fatalf("title = %q, want 我的第一个会话", info.Title)
	}
}

func TestUpdateAfterChat(t *testing.T) {
	m, ctx := testManager(t)

	sid, _, _ := m.Create(ctx, "lark", "user5", "m5")
	m.UpdateAfterChat(ctx, sid, 3)

	info, _ := m.Get(ctx, sid)
	if info.Count != 3 {
		t.Fatalf("count = %d, want 3", info.Count)
	}
}

func TestChannelIsolation(t *testing.T) {
	m, ctx := testManager(t)

	m.Create(ctx, "lark", "user_a", "m1")
	m.Create(ctx, "wechat", "user_a", "m2")

	sessions, _ := m.ListByChannel(ctx, "lark", "user_a")
	for _, s := range sessions {
		if s.ChannelName != "lark" {
			t.Fatal("lark/user_a should only see lark sessions")
		}
	}

	sessions, _ = m.ListByChannel(ctx, "wechat", "user_a")
	for _, s := range sessions {
		if s.ChannelName != "wechat" {
			t.Fatal("wechat/user_a should only see wechat sessions")
		}
	}
}