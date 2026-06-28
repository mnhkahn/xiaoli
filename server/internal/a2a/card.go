package a2a

import (
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// hardcodedAgentCard returns the public Agent Card. The card is hardcoded
// and declares only the three public skills. It MUST NOT leak internal
// details: no MCP tool names, no device tools, no model names, no MCP URLs,
// no device IDs, no admin URLs.
func hardcodedAgentCard(publicBaseURL string) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "Xiaoli",
		Description: "中文个人助理，支持天气查询、日期节假日查询和通用问答。",
		Version:     "1.0.0",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(publicBaseURL+"/a2a", a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities: a2a.AgentCapabilities{
			Streaming:         true,
			PushNotifications: false,
		},
		Skills: []a2a.AgentSkill{
			{
				ID:          "weather_query",
				Name:        "天气查询",
				Description: "查询指定城市的当前天气和今日天气概况。调用方需要在问题中提供城市名称。",
				Tags:        []string{"weather"},
				Examples:    []string{"北京今天天气", "上海天气"},
			},
			{
				ID:          "date_holiday_query",
				Name:        "日期节假日查询",
				Description: "查询今天是什么日子、节假日信息、工作日/休息日、调休安排。",
				Tags:        []string{"date", "holiday"},
				Examples:    []string{"今天是什么日子", "明天放假吗"},
			},
			{
				ID:          "general_qa",
				Name:        "通用问答",
				Description: "回答一般文本问题，提供信息查询、知识解答等服务。",
				Tags:        []string{"qa"},
				Examples:    []string{"介绍一下自己", "什么是 A2A 协议"},
			},
		},
	}
}

// AgentCardHandler returns an http.Handler serving the public Agent Card.
// When publicAgentCard is false the handler returns 404 so the card is not
// discoverable by untrusted callers.
func AgentCardHandler(publicBaseURL string, publicAgentCard bool) http.Handler {
	if !publicAgentCard {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	return a2asrv.NewStaticAgentCardHandler(hardcodedAgentCard(publicBaseURL))
}
