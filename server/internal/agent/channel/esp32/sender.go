package esp32

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentchannel "xiaoli/server/internal/agent/channel"
)

// DeviceHubAdapter is the interface needed to speak to ESP32 devices
// This matches the method signatures from admin.DeviceHub
type DeviceHubAdapter interface {
	Speak(ctx context.Context, deviceID string, text string) (map[string]any, error)
	CallTool(ctx context.Context, deviceID string, toolName string, args map[string]any, timeoutMs int) (any, string, error)
}

// Sender implements Sender interface for ESP32 devices
type Sender struct {
	hub      DeviceHubAdapter
	baseURL  string
	storeArt func(path, displayName, mimeType string, ttl time.Duration) (string, error) // returns URL
}

// NewSender creates an ESP32 sender. The storeArt function takes file info
// and returns the public URL where the artifact can be accessed.
func NewSender(hub DeviceHubAdapter, baseURL string,
	storeArt func(path, displayName, mimeType string, ttl time.Duration) (string, error)) *Sender {
	return &Sender{
		hub:      hub,
		baseURL:  baseURL,
		storeArt: storeArt,
	}
}

func (s *Sender) SendText(ctx context.Context, target agentchannel.SendTarget, text string) error {
	if target.DeviceID == "" {
		return fmt.Errorf("device_id required")
	}
	if text == "" {
		return nil
	}
	_, err := s.hub.Speak(ctx, target.DeviceID, text)
	return err
}

func (s *Sender) SendAttachment(ctx context.Context, target agentchannel.SendTarget, attachment agentchannel.Attachment, caption string) error {
	if target.DeviceID == "" {
		return fmt.Errorf("device_id required")
	}
	if !strings.HasPrefix(attachment.MIMEType, "image/") {
		return fmt.Errorf("esp32 only supports image attachments")
	}

	// Store as artifact and get URL
	url, err := s.storeArt(attachment.Path, attachment.DisplayName, attachment.MIMEType, 24*time.Hour)
	if err != nil {
		return fmt.Errorf("store artifact failed: %w", err)
	}

	// Handle by MIME type
	displayed, err := s.tryDisplayImage(ctx, target.DeviceID, url)
	if err != nil {
		return err
	}
	if displayed && caption == "" {
		return nil
	}

	if caption != "" {
		_, err := s.hub.Speak(ctx, target.DeviceID, caption)
		return err
	}
	return nil
}

func (s *Sender) tryDisplayImage(ctx context.Context, deviceID, url string) (bool, error) {
	// Try to call self.display.show_image_url if available
	_, _, err := s.hub.CallTool(ctx, deviceID, "self.display.show_image_url", map[string]any{
		"url": url,
	}, 5000)
	if err == nil {
		return true, nil
	}
	// Not supported, caller can handle fallback
	return false, nil
}
