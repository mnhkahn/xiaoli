package lark

import (
	"context"
	"strings"
	"testing"
)

type fakeReplySender struct {
	messageID string
	text      string
	title     string
	post      string
}

func (f *fakeReplySender) ReplyText(_ context.Context, messageID string, text string) error {
	f.messageID = messageID
	f.text = text
	return nil
}

func (f *fakeReplySender) ReplyPost(_ context.Context, messageID string, title string, markdown string) error {
	f.messageID = messageID
	f.title = title
	f.post = markdown
	return nil
}

func TestReplyFormatterSendsToLarkMessageAndProvidesInstruction(t *testing.T) {
	sender := &fakeReplySender{}
	formatter := NewReplyFormatter(sender, "message-1", "")

	if got := formatter.Instruction(); !strings.Contains(got, "Markdown") || !strings.Contains(got, "飞书") {
		t.Fatalf("Instruction() = %q, want Lark markdown instruction", got)
	}
	if err := formatter.Send(context.Background(), "# 标题"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sender.messageID != "message-1" {
		t.Fatalf("messageID = %q, want message-1", sender.messageID)
	}
	if sender.post != "# 标题" {
		t.Fatalf("post = %q, want # 标题", sender.post)
	}
}

func TestPostTitleSanitizesUserText(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "小李回复"},
		{"   ", "小李回复"},
		{"你好", "你好"},
		{"/skills", "/skills"},
		{"明天有哪些球赛？列出来。", "明天有哪些球赛？列出来。"},
		{"明天有哪些球赛？列出来。还有NBA赛程呢？", "明天有哪些球赛？列出来。还有NBA赛程呢…"},
		{"a\nb\nc", "a b c"},
		{"  带空格的   ", "带空格的"},
	}
	for _, tt := range tests {
		got := postTitle(tt.input)
		if got != tt.want {
			t.Errorf("postTitle(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPostContentContainsTitle(t *testing.T) {
	got := markdownToPostContent("查看技能", "- skill-a：描述")
	zhCN := got["zh_cn"].(map[string]any)
	if title := zhCN["title"].(string); title != "查看技能" {
		t.Fatalf("title = %q, want 查看技能", title)
	}
}
