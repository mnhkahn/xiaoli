package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentCardHandler_PublicCard(t *testing.T) {
	handler := AgentCardHandler("https://example.com", true)

	req := httptest.NewRequest("GET", "/.well-known/agent-card.json", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var card map[string]any
	err := json.Unmarshal(rr.Body.Bytes(), &card)
	assert.NoError(t, err)

	assert.Equal(t, "Xiaoli", card["name"])
	assert.Contains(t, card, "description")
	assert.Equal(t, "1.0.0", card["version"])

	interfaces := card["supportedInterfaces"].([]any)
	assert.Len(t, interfaces, 1)
	iface := interfaces[0].(map[string]any)
	assert.Equal(t, "https://example.com/a2a", iface["url"])

	skills := card["skills"].([]any)
	assert.Len(t, skills, 3)

	skillIDs := []string{}
	for _, s := range skills {
		skill := s.(map[string]any)
		skillIDs = append(skillIDs, skill["id"].(string))
	}
	assert.Contains(t, skillIDs, "weather_query")
	assert.Contains(t, skillIDs, "date_holiday_query")
	assert.Contains(t, skillIDs, "general_qa")
}

func TestAgentCardHandler_Capabilities(t *testing.T) {
	handler := AgentCardHandler("https://example.com", true)

	req := httptest.NewRequest("GET", "/.well-known/agent-card.json", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var card map[string]any
	json.Unmarshal(rr.Body.Bytes(), &card)

	caps, ok := card["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities not found or wrong type: %T", card["capabilities"])
	}

	// Library omits false values, or sets to false
	if streaming, ok := caps["streaming"].(bool); ok {
		assert.True(t, streaming)
	}
	if push, ok := caps["pushNotifications"].(bool); ok {
		assert.False(t, push)
	}
}

func TestAgentCardHandler_PrivateCardReturns404(t *testing.T) {
	handler := AgentCardHandler("https://example.com", false)

	req := httptest.NewRequest("GET", "/.well-known/agent-card.json", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestAgentCardHandler_NoInternalLeaks(t *testing.T) {
	handler := AgentCardHandler("https://example.com", true)

	req := httptest.NewRequest("GET", "/.well-known/agent-card.json", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	body := rr.Body.String()
	// Must not leak internal identifiers
	for _, secret := range []string{"CYEAM", "AMap", "github", "device", "deviceId", "mcp_servers", "api_key", "secret", "model"} {
		assert.NotContains(t, body, secret)
	}
}
