package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	agentesp32 "xiaoli/server/internal/esp32"
)

type BridgeClient struct {
	baseURL string
	client  *http.Client
}

type Device = agentesp32.Device
type ToolListResponse = agentesp32.ToolListResponse
type BridgeCallRequest = agentesp32.BridgeCallRequest
type BridgeCallResult = agentesp32.BridgeCallResult

func NewBridgeClient(baseURL string, client *http.Client) *BridgeClient {
	if client == nil {
		client = &http.Client{Timeout: 125 * time.Second}
	}
	return &BridgeClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (c *BridgeClient) Devices(ctx context.Context) ([]Device, error) {
	var response struct {
		Devices []Device `json:"devices"`
	}
	if err := c.getJSON(ctx, "/bridge/devices", &response); err != nil {
		return nil, err
	}
	return response.Devices, nil
}

func (c *BridgeClient) Tools(ctx context.Context, deviceID string) (ToolListResponse, error) {
	var response ToolListResponse
	path := "/bridge/tools?device_id=" + url.QueryEscape(deviceID)
	if err := c.getJSON(ctx, path, &response); err != nil {
		return ToolListResponse{}, err
	}
	return response, nil
}

func (c *BridgeClient) Call(ctx context.Context, request BridgeCallRequest) (BridgeCallResult, error) {
	var response BridgeCallResult
	if request.Arguments == nil {
		request.Arguments = map[string]any{}
	}
	if err := c.postJSON(ctx, "/bridge/call", request, &response); err != nil {
		return BridgeCallResult{}, err
	}
	return response, nil
}

func (c *BridgeClient) Speak(ctx context.Context, deviceID string, text string) (map[string]any, error) {
	request := map[string]any{"device_id": deviceID, "text": text}
	var response map[string]any
	if err := c.postJSON(ctx, "/bridge/speak", request, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *BridgeClient) StopSpeak(ctx context.Context, deviceID string) (map[string]any, error) {
	request := map[string]any{"device_id": deviceID}
	var response map[string]any
	if err := c.postJSON(ctx, "/bridge/speak/stop", request, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *BridgeClient) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, out)
}

func (c *BridgeClient) postJSON(ctx context.Context, path string, in any, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, out)
}

func (c *BridgeClient) doJSON(req *http.Request, out any) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		if message := stringValue(payload["error"]); message != "" {
			return fmt.Errorf("bridge %s failed: %s", req.URL.Path, message)
		}
		return fmt.Errorf("bridge %s failed: status %d", req.URL.Path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
