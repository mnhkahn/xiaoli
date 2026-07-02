package wechat

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	agentchannel "github.com/mnhkahn/xiaoli-esp32/internal/agent/channel"
)

// Sender implements Sender interface for WeChat
type Sender struct {
	client *Client
}

func NewSender(c *Client) *Sender {
	return &Sender{client: c}
}

func (s *Sender) SendText(ctx context.Context, target agentchannel.SendTarget, text string) error {
	if text == "" {
		return nil
	}
	if target.UserID == "" {
		return fmt.Errorf("user_id required")
	}
	return SendText(ctx, s.client, "", target.UserID, target.ContextToken, text)
}

func (s *Sender) SendAttachment(ctx context.Context, target agentchannel.SendTarget, attachment agentchannel.Attachment, caption string) error {
	if target.UserID == "" {
		return fmt.Errorf("user_id required")
	}
	if strings.HasPrefix(attachment.MIMEType, "image/") {
		return s.client.SendImageAttachment(ctx, target.UserID, target.ContextToken, attachment.Path, caption)
	}
	fileName := attachment.DisplayName
	if fileName == "" {
		fileName = filepath.Base(attachment.Path)
	}
	return s.client.SendFileAttachment(ctx, target.UserID, target.ContextToken, attachment.Path, fileName, caption)
}
