package wechat

import (
	"context"
	"strings"
	"testing"
)

func TestReplyFormatterSendsWechatTextAndProvidesInstruction(t *testing.T) {
	var sent SendMessageRequest
	c, _ := fakeClientSequence(t, &sent, fakeResponse{
		path:   "/ilink/bot/sendmessage",
		status: 200,
		body:   `{"ret":0}`,
	})
	formatter := NewReplyFormatter(c, "bot-user", "target-user", "ctx-token")

	if got := formatter.Instruction(); !strings.Contains(got, "微信") || !strings.Contains(got, "不要使用 Markdown") {
		t.Fatalf("Instruction() = %q, want WeChat plain text instruction", got)
	}
	if err := formatter.Send(context.Background(), "纯文本"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sent.Msg == nil || sent.Msg.ToUserID != "target-user" || sent.Msg.ContextToken != "ctx-token" {
		t.Fatalf("sent message = %#v, want target and context", sent.Msg)
	}
	if len(sent.Msg.ItemList) != 1 || sent.Msg.ItemList[0].TextItem == nil || sent.Msg.ItemList[0].TextItem.Text != "纯文本" {
		t.Fatalf("sent items = %#v, want text item", sent.Msg.ItemList)
	}
}
