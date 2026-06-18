package esp32

import (
	"encoding/json"
	"testing"
)

func TestParseMCPResultExtractsResultAndError(t *testing.T) {
	result, ok := ParseMCPResult(json.RawMessage(`{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`))
	if !ok {
		t.Fatal("ParseMCPResult(result) ok = false, want true")
	}
	if result.ID != 7 || result.Result.Error != "" {
		t.Fatalf("result = %#v, want id=7 without error", result)
	}
	body, ok := result.Result.Result.(map[string]any)
	if !ok || body["ok"] != true {
		t.Fatalf("result body = %#v, want ok true", result.Result.Result)
	}

	result, ok = ParseMCPResult(json.RawMessage(`{"jsonrpc":"2.0","id":8,"error":{"code":-1,"message":"bad"}}`))
	if !ok {
		t.Fatal("ParseMCPResult(error) ok = false, want true")
	}
	if result.ID != 8 || result.Result.Error != "bad" {
		t.Fatalf("error result = %#v, want id=8 error bad", result)
	}
}

func TestBuildMCPEnvelopeIncludesSessionAndPayload(t *testing.T) {
	envelope := BuildMCPEnvelope("session-1", 3, "tools/list", map[string]any{"withUserTools": true})
	payload, _ := envelope["payload"].(map[string]any)
	if envelope["session_id"] != "session-1" || envelope["type"] != "mcp" {
		t.Fatalf("envelope = %#v, want session and mcp type", envelope)
	}
	if payload["jsonrpc"] != "2.0" || payload["id"] != 3 || payload["method"] != "tools/list" {
		t.Fatalf("payload = %#v, want JSON-RPC tools/list", payload)
	}
}
