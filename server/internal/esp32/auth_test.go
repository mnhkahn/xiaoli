package esp32

import (
	"net/http"
	"testing"
)

func TestDevicePolicyAllowsConfiguredDevicesOnly(t *testing.T) {
	policy := DevicePolicy{AllowedDeviceIDs: []string{"board-1"}}
	if !policy.AllowDevice("board-1") {
		t.Fatal("AllowDevice(board-1) = false, want true")
	}
	if policy.AllowDevice("board-2") {
		t.Fatal("AllowDevice(board-2) = true, want false")
	}
}

func TestDevicePolicyAuthorizesPlainOrBearerToken(t *testing.T) {
	policy := DevicePolicy{AuthEnabled: true, AuthKey: "secret"}

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	if !policy.Authorize(req) {
		t.Fatal("Authorize(Bearer secret) = false, want true")
	}

	req.Header.Set("Authorization", "secret")
	if !policy.Authorize(req) {
		t.Fatal("Authorize(secret) = false, want true")
	}

	req.Header.Set("Authorization", "wrong")
	if policy.Authorize(req) {
		t.Fatal("Authorize(wrong) = true, want false")
	}
}
