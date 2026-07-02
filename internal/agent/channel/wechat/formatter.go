package wechat

import "context"

type ReplyFormatter struct {
	client       *Client
	fromUserID   string
	toUserID     string
	contextToken string
}

func NewReplyFormatter(client *Client, fromUserID, toUserID, contextToken string) ReplyFormatter {
	return ReplyFormatter{
		client:       client,
		fromUserID:   fromUserID,
		toUserID:     toUserID,
		contextToken: contextToken,
	}
}

func (f ReplyFormatter) Instruction() string {
	return "请用适合微信阅读的纯文本回答。可以自然分段，也可以用短横线列表；不要使用 Markdown 标题、粗体、代码块或表格。"
}

func (f ReplyFormatter) Send(ctx context.Context, reply string) error {
	return SendText(ctx, f.client, f.fromUserID, f.toUserID, f.contextToken, reply)
}
