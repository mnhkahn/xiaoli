package esp32

type Device struct {
	DeviceID     string  `json:"device_id"`
	SessionID    string  `json:"session_id,omitempty"`
	ClientIP     string  `json:"client_ip,omitempty"`
	MCPReady     bool    `json:"mcp_ready"`
	ToolCount    int     `json:"tool_count"`
	ConnectedAt  float64 `json:"connected_at,omitempty"`
	LastActivity float64 `json:"last_activity,omitempty"`
}

type ToolListResponse struct {
	Tools []map[string]any `json:"tools"`
	Ready bool             `json:"ready"`
}

type BridgeCallRequest struct {
	DeviceID  string         `json:"device_id"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	Timeout   int            `json:"timeout,omitempty"`
}

type BridgeCallResult struct {
	OK        bool   `json:"ok"`
	Result    any    `json:"result,omitempty"`
	Raw       string `json:"raw,omitempty"`
	Error     string `json:"error,omitempty"`
	ElapsedMS int    `json:"elapsed_ms,omitempty"`
}

type MCPCallResult struct {
	Result any
	Raw    string
	Error  string
}
