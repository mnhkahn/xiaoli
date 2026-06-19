package lark

import "context"

type ReplySender interface {
	ReplyText(ctx context.Context, messageID string, text string) error
}

type ReplyFormatter struct {
	sender    ReplySender
	messageID string
}

func NewReplyFormatter(sender ReplySender, messageID string) ReplyFormatter {
	return ReplyFormatter{sender: sender, messageID: messageID}
}

func (f ReplyFormatter) Instruction() string {
	return "请用适合飞书富文本展示的 Markdown 回答。可以使用清晰的标题、短列表和链接；不要使用复杂表格。"
}

func (f ReplyFormatter) Send(ctx context.Context, reply string) error {
	return f.sender.ReplyText(ctx, f.messageID, reply)
}
