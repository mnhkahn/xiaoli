package session

import (
	"context"
	"testing"
	"time"

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

func TestListRecentUsesSortedIndex(t *testing.T) {
	m, ctx := testManager(t)

	first, _, err := m.Create(ctx, "lark", "user_a", "m1")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, _, err := m.Create(ctx, "wechat", "user_b", "m2")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if err := m.client.ZAdd(ctx, m.recentKey(), redis.Z{Score: 1, Member: first}, redis.Z{Score: 2, Member: second}).Err(); err != nil {
		t.Fatalf("set recent scores: %v", err)
	}

	sessions, err := m.ListRecent(ctx, 1)
	if err != nil {
		t.Fatalf("ListRecent failed: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != second {
		t.Fatalf("ListRecent(1) = %#v, want %q", sessions, second)
	}
}

func TestListRecentSkipsExpiredMetadata(t *testing.T) {
	m, ctx := testManager(t)
	sid, _, err := m.Create(ctx, "lark", "user_a", "m1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.client.Del(ctx, m.metaKey(sid)).Err(); err != nil {
		t.Fatalf("delete metadata: %v", err)
	}

	sessions, err := m.ListRecent(ctx, 20)
	if err != nil {
		t.Fatalf("ListRecent failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("ListRecent() = %#v, want no stale sessions", sessions)
	}
}

func TestBackfillRecentCreatesIndex(t *testing.T) {
	m, ctx := testManager(t)
	first, _, err := m.Create(ctx, "lark", "user_a", "m1")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, _, err := m.Create(ctx, "wechat", "user_b", "m2")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if err := m.client.Del(ctx, m.recentKey()).Err(); err != nil {
		t.Fatalf("delete index: %v", err)
	}
	firstAt := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	secondAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if err := m.client.HSet(ctx, m.metaKey(first), "updated_at", firstAt).Err(); err != nil {
		t.Fatalf("update first timestamp: %v", err)
	}
	if err := m.client.HSet(ctx, m.metaKey(second), "updated_at", secondAt).Err(); err != nil {
		t.Fatalf("update second timestamp: %v", err)
	}

	if err := m.BackfillRecent(ctx); err != nil {
		t.Fatalf("BackfillRecent failed: %v", err)
	}
	ids, err := m.client.ZRevRange(ctx, m.recentKey(), 0, -1).Result()
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if len(ids) != 2 || ids[0] != second || ids[1] != first {
		t.Fatalf("backfilled ids = %#v, want [%q %q]", ids, second, first)
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

func TestLoadMessages(t *testing.T) {
	m, ctx := testManager(t)

	// Save messages to the key that Memory.Save uses (prefix + sessionID)
	raw := `[{"role":"user","content":"你好"},{"role":"assistant","content":"你好！有什么可以帮助你的？"}]`
	if err := m.client.Set(ctx, m.messageKey("ses_test123"), raw, 0).Err(); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	msgs := m.LoadMessages(ctx, "ses_test123")
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "你好" {
		t.Fatalf("msg[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "你好！有什么可以帮助你的？" {
		t.Fatalf("msg[1] = %+v", msgs[1])
	}
}
