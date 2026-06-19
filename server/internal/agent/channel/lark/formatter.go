package lark

import (
	"context"
	"strings"
	"unicode/utf8"
)

type ReplySender interface {
	ReplyPost(ctx context.Context, messageID string, title string, markdown string) error
}

type ReplyFormatter struct {
	sender    ReplySender
	messageID string
	userText  string
}

func NewReplyFormatter(sender ReplySender, messageID string, userText string) ReplyFormatter {
	return ReplyFormatter{sender: sender, messageID: messageID, userText: userText}
}

func (f ReplyFormatter) Instruction() string {
	return "请用适合飞书富文本展示的 Markdown 回答。可以使用清晰的标题、短列表和链接；不要使用复杂表格。"
}

func (f ReplyFormatter) Send(ctx context.Context, reply string) error {
	return f.sender.ReplyPost(ctx, f.messageID, postTitle(f.userText), reply)
}

func postTitle(userText string) string {
	title := strings.TrimSpace(userText)
	title = strings.ReplaceAll(title, "\n", " ")
	title = strings.ReplaceAll(title, "\r", "")
	title = strings.TrimSpace(title)
if title == "" {
		return "小李回复"
	}
	if utf8.RuneCountInString(title) > 20 {
		title = string([]rune(title)[:20]) + "…"
	}
	return title
}
