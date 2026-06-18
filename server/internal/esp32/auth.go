package esp32

import (
	"net/http"
	"strings"
)

type DevicePolicy struct {
	AllowedDeviceIDs []string
	AuthEnabled      bool
	AuthKey          string
}

func (p DevicePolicy) AllowDevice(deviceID string) bool {
	if len(p.AllowedDeviceIDs) == 0 {
		return true
	}
	for _, allowed := range p.AllowedDeviceIDs {
		if allowed == deviceID {
			return true
		}
	}
	return false
}

func (p DevicePolicy) Authorize(r *http.Request) bool {
	if !p.AuthEnabled {
		return true
	}
	if p.AuthKey == "" {
		return false
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	return auth == p.AuthKey || auth == "Bearer "+p.AuthKey
}
