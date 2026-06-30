package esp32

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentchannel "xiaoli/server/internal/agent/channel"
)

type fakeDeviceHub struct {
	speakCalls int
	toolCalls  int
}

func (f *fakeDeviceHub) Speak(context.Context, string, string) (map[string]any, error) {
	f.speakCalls++
	return nil, nil
}

func (f *fakeDeviceHub) CallTool(context.Context, string, string, map[string]any, int) (any, string, error) {
	f.toolCalls++
	return nil, "", nil
}

func TestSendAttachmentRejectsNonImageWithoutStoring(t *testing.T) {
	storeCalls := 0
	sender := NewSender(&fakeDeviceHub{}, "https://server.test", func(path, displayName, mimeType string, ttl time.Duration) (string, error) {
		storeCalls++
		return "https://server.test/artifacts/file.pdf", nil
	})

	err := sender.SendAttachment(context.Background(), agentchannel.SendTarget{
		Channel:  agentchannel.TypeESP32,
		DeviceID: "device-1",
	}, agentchannel.Attachment{
		Path:        "/tmp/report.pdf",
		DisplayName: "report.pdf",
		MIMEType:    "application/pdf",
	}, "报告")
	if err == nil {
		t.Fatal("SendAttachment() error = nil, want non-image rejection")
	}
	if !strings.Contains(err.Error(), "only supports image") {
		t.Fatalf("SendAttachment() error = %v, want only supports image", err)
	}
	if storeCalls != 0 {
		t.Fatalf("store calls = %d, want 0", storeCalls)
	}
}

func TestSendAttachmentStoresAndDisplaysImage(t *testing.T) {
	hub := &fakeDeviceHub{}
	sender := NewSender(hub, "https://server.test", func(path, displayName, mimeType string, ttl time.Duration) (string, error) {
		if path != "/tmp/photo.png" || displayName != "photo.png" || mimeType != "image/png" {
			t.Fatalf("store args = %q %q %q", path, displayName, mimeType)
		}
		return "https://server.test/artifacts/photo.png", nil
	})

	err := sender.SendAttachment(context.Background(), agentchannel.SendTarget{
		Channel:  agentchannel.TypeESP32,
		DeviceID: "device-1",
	}, agentchannel.Attachment{
		Path:        "/tmp/photo.png",
		DisplayName: "photo.png",
		MIMEType:    "image/png",
	}, "图片")
	if err != nil {
		t.Fatalf("SendAttachment() error = %v", err)
	}
	if hub.toolCalls != 1 || hub.speakCalls != 1 {
		t.Fatalf("tool/speak calls = %d/%d, want 1/1", hub.toolCalls, hub.speakCalls)
	}
}

func TestSendAttachmentReturnsStoreImageError(t *testing.T) {
	sender := NewSender(&fakeDeviceHub{}, "https://server.test", func(path, displayName, mimeType string, ttl time.Duration) (string, error) {
		return "", errors.New("store failed")
	})

	err := sender.SendAttachment(context.Background(), agentchannel.SendTarget{
		Channel:  agentchannel.TypeESP32,
		DeviceID: "device-1",
	}, agentchannel.Attachment{
		Path:        "/tmp/photo.png",
		DisplayName: "photo.png",
		MIMEType:    "image/png",
	}, "")
	if err == nil || !strings.Contains(err.Error(), "store artifact failed") {
		t.Fatalf("SendAttachment() error = %v, want store artifact failed", err)
	}
}
