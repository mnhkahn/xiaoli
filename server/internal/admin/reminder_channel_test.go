package admin

import (
	"context"
	"errors"
	"testing"
	"time"

	agentsession "github.com/mnhkahn/xiaoli/internal/agent/session"
	agentworkflow "github.com/mnhkahn/xiaoli/internal/agent/workflow"
)

func TestSettingsReminderDefaultChannelDefaultsToLark(t *testing.T) {
	got := (settingsConfig{}).reminderDefaultChannel()
	if got != "lark" {
		t.Fatalf("reminderDefaultChannel() = %q, want %q", got, "lark")
	}
}

func TestSettingsReminderDefaultChannelNormalizesAliases(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "lark", raw: "lark", want: "lark"},
		{name: "lark text", raw: "lark_text", want: "lark"},
		{name: "wechat text", raw: "wechat_text", want: "wechat"},
		{name: "device voice", raw: "device_voice", want: "esp32"},
		{name: "unknown falls back", raw: "pagerduty", want: "lark"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (settingsConfig{
				Reminder: settingsReminder{
					DefaultChannel: tt.raw,
				},
			}).reminderDefaultChannel()
			if got != tt.want {
				t.Fatalf("reminderDefaultChannel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReminderDeliveryChannelUsesConfiguredDefaultForEmptyDefinitionChannel(t *testing.T) {
	cfg := testConfig()
	cfg.ReminderDefaultChannel = "esp32"
	srv := NewServer(cfg)

	if got := srv.reminderDeliveryChannel(""); got != "esp32" {
		t.Fatalf("reminderDeliveryChannel(\"\") = %q, want %q", got, "esp32")
	}
}

func TestReminderDeliveryChannelKeepsDefinitionChannel(t *testing.T) {
	cfg := testConfig()
	cfg.ReminderDefaultChannel = "lark"
	srv := NewServer(cfg)

	if got := srv.reminderDeliveryChannel("wechat"); got != "wechat" {
		t.Fatalf("reminderDeliveryChannel(\"wechat\") = %q, want %q", got, "wechat")
	}
}

func TestReminderDeliveryChannelsQueuesUnsupportedWechat(t *testing.T) {
	cfg := testConfig()
	cfg.ReminderDefaultChannel = "lark"
	srv := NewServer(cfg)

	got := srv.reminderDeliveryChannels("wechat")
	want := []string(nil)
	if len(got) != len(want) {
		t.Fatalf("reminderDeliveryChannels(wechat) = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reminderDeliveryChannels(wechat) = %#v, want %#v", got, want)
		}
	}
}

func TestWechatReminderIsQueuedInsteadOfFallingBackToLark(t *testing.T) {
	cfg := testConfig()
	cfg.DataDir = t.TempDir()
	srv := NewServer(cfg)
	scheduledAt := time.Date(2026, 7, 16, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))
	def := agentworkflow.Definition{
		ID:       "wechat-reminder",
		Name:     "喝水",
		Channel:  "wechat",
		SenderID: "wechat-user",
		Metadata: map[string]any{"text": "喝水"},
	}

	if err := srv.sendReminderByChannel(context.Background(), def, scheduledAt); err != nil {
		t.Fatalf("sendReminderByChannel() error = %v", err)
	}
	items, err := srv.pendingReminderStore().Load()
	if err != nil {
		t.Fatalf("pending reminder Load() error = %v", err)
	}
	if len(items) != 1 || items[0].SenderID != "wechat-user" || items[0].Text != "喝水" {
		t.Fatalf("pending reminders = %#v, want one wechat reminder", items)
	}
}

func TestDeliverPendingWechatRemindersKeepsFailuresAndContinues(t *testing.T) {
	cfg := testConfig()
	cfg.DataDir = t.TempDir()
	srv := NewServer(cfg)
	for _, id := range []string{"first", "second", "cancelled"} {
		if err := srv.reminderStore().Add(agentworkflow.Reminder{ID: id, Enabled: id != "cancelled"}); err != nil {
			t.Fatalf("add reminder %q: %v", id, err)
		}
	}
	items := []agentworkflow.PendingReminder{
		{ID: "one", ReminderID: "first", SenderID: "wechat-user", Text: "第一条", ScheduledAt: "2026-07-16T09:00:00+08:00"},
		{ID: "two", ReminderID: "second", SenderID: "wechat-user", Text: "第二条", ScheduledAt: "2026-07-16T10:00:00+08:00"},
		{ID: "three", ReminderID: "cancelled", SenderID: "wechat-user", Text: "已取消", ScheduledAt: "2026-07-16T11:00:00+08:00"},
	}
	for _, item := range items {
		if err := srv.pendingReminderStore().Enqueue(item); err != nil {
			t.Fatalf("enqueue %q: %v", item.ID, err)
		}
	}
	var sent []string
	srv.deliverPendingWechatReminders(context.Background(), "wechat-user", func(_ context.Context, text string) error {
		sent = append(sent, text)
		if len(sent) == 1 {
			return errors.New("temporary failure")
		}
		return nil
	})
	if len(sent) != 2 {
		t.Fatalf("sent = %#v, want failed first and successful second", sent)
	}
	remaining, err := srv.pendingReminderStore().Load()
	if err != nil {
		t.Fatalf("pending reminder Load() error = %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != "one" {
		t.Fatalf("remaining = %#v, want only failed first reminder", remaining)
	}
}

func TestReminderLarkTargetUsesChannelOpenID(t *testing.T) {
	target, err := reminderLarkTargetFromChannels([]agentsession.ChannelEntry{
		{ChannelName: string(ChannelWechatText), ChannelUser: "wechat-user"},
		{ChannelName: string(ChannelLarkText), ChannelUser: "ou_lark_user"},
	})
	if err != nil {
		t.Fatalf("reminderLarkTargetFromChannels() error = %v", err)
	}
	if target != "ou_lark_user" {
		t.Fatalf("reminderLarkTargetFromChannels() = %q, want lark open ID", target)
	}
}

func TestReminderLarkTargetRejectsAmbiguousChannels(t *testing.T) {
	_, err := reminderLarkTargetFromChannels([]agentsession.ChannelEntry{
		{ChannelName: string(ChannelLarkText), ChannelUser: "ou_first"},
		{ChannelName: string(ChannelLarkText), ChannelUser: "ou_second"},
	})
	if err == nil {
		t.Fatal("reminderLarkTargetFromChannels() error = nil, want ambiguity error")
	}
}
