package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentCardHandler_Structure(t *testing.T) {
	handler := AgentCardHandler("https://example.com")

	req := httptest.NewRequest("GET", "/.well-known/agent-card.json", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var card map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &card)
	assert.NoError(t, err)

	assert.Equal(t, "Xiaoli", card["name"])
	assert.Contains(t, card, "description")
	assert.Equal(t, "https://example.com/a2a", card["url"])
	assert.Equal(t, "1.0.0", card["version"])

	skills := card["skills"].([]interface{})
	assert.Len(t, skills, 3)

	skillIDs := []string{}
	for _, s := range skills {
		skill := s.(map[string]interface{})
		skillIDs = append(skillIDs, skill["id"].(string))
	}
	assert.Contains(t, skillIDs, "weather_query")
	assert.Contains(t, skillIDs, "date_holiday_query")
	assert.Contains(t, skillIDs, "general_qa")
}

func TestAgentCardHandler_Capabilities(t *testing.T) {
	handler := AgentCardHandler("https://example.com")

	req := httptest.NewRequest("GET", "/.well-known/agent-card.json", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var card map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &card)

	caps := card["capabilities"].(map[string]interface{})
	assert.True(t, caps["sendMessage"].(bool))
	assert.True(t, caps["getTask"].(bool))
	assert.False(t, caps["listTasks"].(bool))
	assert.False(t, caps["cancelTask"].(bool))
	assert.False(t, caps["streaming"].(bool))
}
