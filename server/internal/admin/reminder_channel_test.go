package admin

import (
	"testing"

	agentsession "github.com/mnhkahn/xiaoli/internal/agent/session"
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

func TestReminderDeliveryChannelsSkipsUnsupportedWechat(t *testing.T) {
	cfg := testConfig()
	cfg.ReminderDefaultChannel = "lark"
	srv := NewServer(cfg)

	got := srv.reminderDeliveryChannels("wechat")
	want := []string{"lark"}
	if len(got) != len(want) {
		t.Fatalf("reminderDeliveryChannels(wechat) = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reminderDeliveryChannels(wechat) = %#v, want %#v", got, want)
		}
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
