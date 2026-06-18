package esp32

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type ParsedMCPResult struct {
	ID     int
	Result MCPCallResult
}

func BuildMCPEnvelope(sessionID string, id int, method string, params map[string]any) map[string]any {
	return map[string]any{
		"session_id": sessionID,
		"type":       "mcp",
		"payload": map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
			"params":  params,
		},
	}
}

func BuildInitializeParams(publicBaseURL, token string) map[string]any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"vision": map[string]any{
				"url":   publicBaseURL,
				"token": token,
			},
		},
		"clientInfo": map[string]any{"name": "xiaoli-go-admin", "version": "0.1.0"},
	}
}

func BuildToolsListParams(cursor string) map[string]any {
	params := map[string]any{"withUserTools": true}
	if cursor != "" {
		params["cursor"] = cursor
	}
	return params
}

func BuildToolsCallParams(toolName string, args map[string]any) map[string]any {
	return map[string]any{
		"name":      toolName,
		"arguments": args,
	}
}

func ParseMCPResult(raw json.RawMessage) (ParsedMCPResult, bool) {
	if len(raw) == 0 {
		return ParsedMCPResult{}, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ParsedMCPResult{}, false
	}
	id, ok := jsonNumberID(payload["id"])
	if !ok {
		return ParsedMCPResult{}, false
	}
	if _, hasResult := payload["result"]; hasResult {
		return ParsedMCPResult{
			ID:     id,
			Result: MCPCallResult{Result: decodeAny(payload["result"]), Raw: CompactJSON(payload["result"])},
		}, true
	}
	if _, hasError := payload["error"]; hasError {
		return ParsedMCPResult{
			ID:     id,
			Result: MCPCallResult{Error: MCPErrorMessage(payload["error"]), Raw: CompactJSON(payload["error"])},
		}, true
	}
	return ParsedMCPResult{}, false
}

func Method(raw json.RawMessage) string {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	var method string
	_ = json.Unmarshal(payload["method"], &method)
	return method
}

func AnySlice(value any) []any {
	if items, ok := value.([]any); ok {
		return items
	}
	return nil
}

func CompactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) == nil {
		return buf.String()
	}
	return string(raw)
}

func MCPErrorMessage(raw json.RawMessage) string {
	var payload struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	}
	if json.Unmarshal(raw, &payload) == nil && payload.Message != "" {
		return payload.Message
	}
	return CompactJSON(raw)
}

func jsonNumberID(raw json.RawMessage) (int, bool) {
	var id int
	if err := json.Unmarshal(raw, &id); err == nil {
		return id, true
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		i, err := num.Int64()
		return int(i), err == nil
	}
	return 0, false
}

func decodeAny(raw json.RawMessage) any {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Sprintf("%s", raw)
	}
	return value
}
