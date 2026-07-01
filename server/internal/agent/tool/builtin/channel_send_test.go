package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	agentchannel "github.com/mnhkahn/xiaoli-esp32/server/internal/agent/channel"
)

type fakeChannelSender struct {
	textTarget       agentchannel.SendTarget
	text             string
	attachmentTarget agentchannel.SendTarget
	attachment       agentchannel.Attachment
	caption          string
}

func (f *fakeChannelSender) SendText(_ context.Context, target agentchannel.SendTarget, text string) error {
	f.textTarget = target
	f.text = text
	return nil
}

func (f *fakeChannelSender) SendAttachment(_ context.Context, target agentchannel.SendTarget, attachment agentchannel.Attachment, caption string) error {
	f.attachmentTarget = target
	f.attachment = attachment
	f.caption = caption
	return nil
}

func TestChannelSendToolSendsAttachmentToCurrentChannel(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "练习纸.pdf")
	if err := os.WriteFile(filePath, []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sender := &fakeChannelSender{}
	tb := NewChannelSendTool(ChannelSendConfig{
		Lark:         sender,
		AllowedRoots: []string{dir},
	})
	inv, ok := tb.(tool.InvokableTool)
	if !ok {
		t.Fatal("channel_send should be invokable")
	}
	ctx := agentchannel.WithSendTarget(context.Background(), agentchannel.SendTarget{
		Channel:          agentchannel.TypeLark,
		UserID:           "ou_user",
		ChatID:           "oc_chat",
		ReplyToMessageID: "om_message",
	})
	ctx, status := NewChannelSendStatus(ctx)

	got, err := inv.InvokableRun(ctx, `{"target":"current","file_path":"`+filePath+`","display_name":"练习纸.pdf","mime_type":"application/pdf","text":"已生成"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("result is not JSON: %q", got)
	}
	if payload["ok"] != true {
		t.Fatalf("result = %s, want ok", got)
	}
	if sender.attachmentTarget.UserID != "ou_user" || sender.attachmentTarget.ReplyToMessageID != "om_message" {
		t.Fatalf("target = %#v, want current Lark target", sender.attachmentTarget)
	}
	if sender.attachment.Path != filePath || sender.attachment.DisplayName != "练习纸.pdf" || sender.attachment.MIMEType != "application/pdf" {
		t.Fatalf("attachment = %#v, want file path and display name", sender.attachment)
	}
	if sender.caption != "已生成" {
		t.Fatalf("caption = %q, want 已生成", sender.caption)
	}
	if !status.Sent() {
		t.Fatal("status.Sent() = false, want true after successful send")
	}
}
