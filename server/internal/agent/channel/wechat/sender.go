package wechat

import (
	"context"
	"fmt"

	agentchannel "xiaoli/server/internal/agent/channel"
)

// Sender implements Sender interface for WeChat
// Note: WeChat currently only supports text via channel_send
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

var ErrAttachmentNotSupported = fmt.Errorf("attachments not supported by WeChat channel")

func (s *Sender) SendAttachment(ctx context.Context, target agentchannel.SendTarget, attachment agentchannel.Attachment, caption string) error {
	// Send caption as text if provided
	if caption != "" {
		if err := s.SendText(ctx, target, caption); err != nil {
			return err
		}
	}
	// WeChat does not support file uploads via bot yet
	return ErrAttachmentNotSupported
}
