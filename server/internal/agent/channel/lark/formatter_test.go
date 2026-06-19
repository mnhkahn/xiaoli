package lark

import (
	"context"
	"strings"
	"testing"
)

type fakeReplySender struct {
	messageID string
	reply     string
}

func (f *fakeReplySender) ReplyText(_ context.Context, messageID string, text string) error {
	f.messageID = messageID
	f.reply = text
	return nil
}

func TestReplyFormatterSendsToLarkMessageAndProvidesInstruction(t *testing.T) {
	sender := &fakeReplySender{}
	formatter := NewReplyFormatter(sender, "message-1")

	if got := formatter.Instruction(); !strings.Contains(got, "Markdown") || !strings.Contains(got, "飞书") {
		t.Fatalf("Instruction() = %q, want Lark markdown instruction", got)
	}
	if err := formatter.Send(context.Background(), "# 标题"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sender.messageID != "message-1" || sender.reply != "# 标题" {
		t.Fatalf("sender = %#v, want message id and reply", sender)
	}
}
