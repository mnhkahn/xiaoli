package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DeviceRegistry persists Android device ownership and hashes, rather than raw,
// device tokens. Legacy ESP32 credentials remain handled by HubConfig.
type DeviceRegistry struct {
	mu       sync.Mutex
	path     string
	now      func() time.Time
	devices  map[string]pairedDevice
	pairings map[string]devicePairing
}

type pairedDevice struct {
	DeviceID  string    `json:"device_id"`
	Owner     string    `json:"owner"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	TokenHash string    `json:"token_hash"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type devicePairing struct {
	Owner     string
	ExpiresAt time.Time
}

type persistedDevices struct {
	Devices []pairedDevice `json:"devices"`
}

func newDeviceRegistry(path string, now func() time.Time) *DeviceRegistry {
	if now == nil {
		now = time.Now
	}
	r := &DeviceRegistry{path: path, now: now, devices: map[string]pairedDevice{}, pairings: map[string]devicePairing{}}
	if path == "" {
		return r
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r
	}
	if err != nil || json.Unmarshal(body, &persistedDevices{}) != nil {
		return r
	}
	var saved persistedDevices
	if json.Unmarshal(body, &saved) == nil {
		for _, device := range saved.Devices {
			if device.DeviceID != "" {
				r.devices[device.DeviceID] = device
			}
		}
	}
	return r
}

func (r *DeviceRegistry) CreatePairing(owner string) (code string, expiresAt time.Time, err error) {
	if strings.TrimSpace(owner) == "" {
		return "", time.Time{}, errors.New("missing Logto user subject")
	}
	code, err = randomDeviceSecret(24)
	if err != nil {
		return "", time.Time{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	expiresAt = r.now().Add(5 * time.Minute)
	r.pairings[code] = devicePairing{Owner: owner, ExpiresAt: expiresAt}
	return code, expiresAt, nil
}

func (r *DeviceRegistry) Claim(code, deviceID, name, kind string) (pairedDevice, string, error) {
	deviceID = strings.TrimSpace(deviceID)
	if !validPairedDeviceID(deviceID) {
		return pairedDevice{}, "", errors.New("invalid device_id")
	}
	if kind != "android" {
		return pairedDevice{}, "", errors.New("only android pairing is supported")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pairing, ok := r.pairings[code]
	if !ok || !r.now().Before(pairing.ExpiresAt) {
		delete(r.pairings, code)
		return pairedDevice{}, "", errors.New("pairing code is invalid or expired")
	}
	delete(r.pairings, code)
	token, err := randomDeviceSecret(32)
	if err != nil {
		return pairedDevice{}, "", err
	}
	now := r.now()
	device := r.devices[deviceID]
	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}
	device.DeviceID, device.Owner, device.Name, device.Kind = deviceID, pairing.Owner, strings.TrimSpace(name), kind
	device.TokenHash, device.Enabled, device.UpdatedAt = hashDeviceToken(token), true, now
	r.devices[deviceID] = device
	if err := r.saveLocked(); err != nil {
		return pairedDevice{}, "", err
	}
	return device, token, nil
}

// Authorize returns known=false for legacy ESP32 IDs, allowing their existing
// static policy to remain unchanged.
func (r *DeviceRegistry) Authorize(deviceID, authorization string) (known, authorized bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, known := r.devices[deviceID]
	if !known {
		return false, false
	}
	token := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(authorization), "Bearer "))
	return true, device.Enabled && device.TokenHash != "" && subtleEqual(device.TokenHash, hashDeviceToken(token))
}

func (r *DeviceRegistry) Owns(owner, deviceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, known := r.devices[deviceID]
	return known && device.Owner == owner
}

func (r *DeviceRegistry) IsKnown(deviceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.devices[deviceID]
	return ok
}

func (r *DeviceRegistry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	devices := make([]pairedDevice, 0, len(r.devices))
	for _, device := range r.devices {
		devices = append(devices, device)
	}
	body, err := json.Marshal(persistedDevices{Devices: devices})
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, body, 0o600)
}

func validPairedDeviceID(value string) bool {
	if len(value) < 3 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == ':') {
			return false
		}
	}
	return true
}

func randomDeviceSecret(bytes int) (string, error) {
	body := make([]byte, bytes)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func hashDeviceToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func subtleEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var diff byte
	for index := range left {
		diff |= left[index] ^ right[index]
	}
	return diff == 0
}
