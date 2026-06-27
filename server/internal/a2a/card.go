package a2a

import (
	"encoding/json"
	"net/http"
)

// AgentSkill represents a capability declared in the Agent Card
type AgentSkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// AgentCard is the discovery document for A2A protocol
type AgentCard struct {
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	URL                string          `json:"url"`
	Version            string          `json:"version"`
	ProtocolVersion    string          `json:"protocolVersion"`
	DefaultInputModes  []string        `json:"defaultInputModes"`
	DefaultOutputModes []string        `json:"defaultOutputModes"`
	Capabilities       map[string]bool `json:"capabilities"`
	Skills             []AgentSkill    `json:"skills"`
}

// AgentCardHandler returns the hardcoded Agent Card for discovery
func AgentCardHandler(publicBaseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		card := AgentCard{
			Name:        "Xiaoli",
			Description: "中文个人助理，支持天气查询、日期节假日查询和通用问答。",
			URL:         publicBaseURL + "/a2a",
			Version:     "1.0.0",
			ProtocolVersion:    "1.0",
			DefaultInputModes:  []string{"text/plain"},
			DefaultOutputModes: []string{"text/plain"},
			Capabilities: map[string]bool{
				"sendMessage":      true,
				"getTask":          true,
				"listTasks":        false,
				"cancelTask":       false,
				"streaming":        false,
				"pushNotification": false,
			},
			Skills: []AgentSkill{
				{
					ID:          "weather_query",
					Name:        "天气查询",
					Description: "查询指定城市的当前天气和今日天气概况。调用方需要在问题中提供城市名称。",
				},
				{
					ID:          "date_holiday_query",
					Name:        "日期节假日查询",
					Description: "查询今天是什么日子、节假日信息、工作日/休息日、调休安排。",
				},
				{
					ID:          "general_qa",
					Name:        "通用问答",
					Description: "回答一般文本问题，提供信息查询、知识解答等服务。",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(card)
	}
}
