package channel

import (
	"context"
	"testing"
)

type staticProvider []Info

func (p staticProvider) ListChannels(context.Context) ([]Info, error) {
	return []Info(p), nil
}

func TestRegistryListsChannelsInStableTypeOrder(t *testing.T) {
	registry := NewRegistry(
		staticProvider{{ID: "wechat:bot", Type: TypeWechat}},
		staticProvider{{ID: "esp32:device-2", Type: TypeESP32}, {ID: "esp32:device-1", Type: TypeESP32}},
		staticProvider{{ID: "lark:app:cli", Type: TypeLark}},
	)

	channels, err := registry.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	got := []string{channels[0].ID, channels[1].ID, channels[2].ID, channels[3].ID}
	want := []string{"esp32:device-1", "esp32:device-2", "lark:app:cli", "wechat:bot"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("channels order = %#v, want %#v", got, want)
		}
	}
}

func TestESP32InfoFromDevice(t *testing.T) {
	info := ESP32InfoFromDevice(DeviceInfo{
		DeviceID:     "device-1",
		MCPReady:     true,
		LastActivity: 123,
	})

	if info.ID != "esp32:device-1" || info.Type != TypeESP32 || info.DeviceID != "device-1" {
		t.Fatalf("info = %#v, want esp32 channel for device-1", info)
	}
	if !info.Capabilities.Tools || !info.Capabilities.Audio || !info.Capabilities.Video {
		t.Fatalf("capabilities = %#v, want ESP32 media/tools enabled", info.Capabilities)
	}
}
