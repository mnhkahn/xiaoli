package lark

import (
	"context"
	"fmt"
	"strings"

	agentchannel "github.com/mnhkahn/xiaoli/internal/agent/channel"
)

// Sender implements Sender interface for Lark
type Sender struct {
	client  *Client
	baseURL string // For building artifact URLs if needed
}

func NewSender(c *Client, baseURL string) *Sender {
	return &Sender{
		client:  c,
		baseURL: baseURL,
	}
}

func (s *Sender) SendText(ctx context.Context, target agentchannel.SendTarget, text string) error {
	if text == "" {
		return nil
	}

	// If we have a message to reply to, use ReplyText; otherwise CreateTextMessage
	if target.ReplyToMessageID != "" {
		return s.client.ReplyText(ctx, target.ReplyToMessageID, text)
	}

	// Prefer user_id over chat_id for direct messages
	if target.UserID != "" {
		return s.client.CreateTextMessage(ctx, target.UserID, text)
	}
	if target.ChatID != "" {
		return s.client.CreateTextMessage(ctx, target.ChatID, text)
	}

	return fmt.Errorf("no target specified (user_id or chat_id required)")
}

func (s *Sender) SendAttachment(ctx context.Context, target agentchannel.SendTarget, attachment agentchannel.Attachment, caption string) error {
	receiveID := target.UserID
	if receiveID == "" {
		receiveID = target.ChatID
	}
	if receiveID == "" {
		return fmt.Errorf("no target user/chat specified")
	}

	// Check MIME type and choose appropriate upload method
	if strings.HasPrefix(attachment.MIMEType, "image/") {
		// Image upload
		imageKey, err := s.client.UploadImage(ctx, attachment.Path)
		if err != nil {
			return fmt.Errorf("upload image failed: %w", err)
		}
		// Send caption first if present
		if caption != "" {
			if err := s.SendText(ctx, target, caption); err != nil {
				return err
			}
		}
		if target.ReplyToMessageID != "" {
			return s.client.ReplyImage(ctx, target.ReplyToMessageID, imageKey)
		}
		return s.client.SendImage(ctx, receiveID, imageKey)
	}

	// All other files use file upload
	fileKey, err := s.client.UploadFile(ctx, attachment.Path, attachment.DisplayName)
	if err != nil {
		return fmt.Errorf("upload file failed: %w", err)
	}
	if caption != "" {
		if err := s.SendText(ctx, target, caption); err != nil {
			return err
		}
	}
	if target.ReplyToMessageID != "" {
		return s.client.ReplyFile(ctx, target.ReplyToMessageID, fileKey)
	}
	return s.client.SendFile(ctx, receiveID, fileKey)
}
