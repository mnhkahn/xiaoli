package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	a2a "github.com/mnhkahn/xiaoli/server/internal/a2a"
)

const (
	geekNewsMinimumItems = 5
	geekNewsMaximumItems = 12
)

// geekNewsFetcher is deliberately narrow: the A2A server always invokes the
// fixed news command and never lets model text influence a shell command.
type geekNewsFetcher interface {
	Fetch(context.Context, string) (geekNewsReply, error)
}

type cyeamGeekNewsFetcher struct{}

func newCYEAMGeekNewsFetcher() geekNewsFetcher { return cyeamGeekNewsFetcher{} }

func (cyeamGeekNewsFetcher) Fetch(ctx context.Context, date string) (geekNewsReply, error) {
	cmd := exec.CommandContext(ctx, "cyeam", "news", "get", "--date", date)
	output, err := cmd.Output()
	if err != nil {
		return geekNewsReply{}, fmt.Errorf("fetch news with cyeam: %w", err)
	}
	return decodeCYEAMGeekNews(output)
}

func decodeCYEAMGeekNews(raw []byte) (geekNewsReply, error) {
	var envelope struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return geekNewsReply{}, fmt.Errorf("decode cyeam response: %w", err)
	}
	if !envelope.OK {
		return geekNewsReply{}, errors.New("cyeam news returned ok=false")
	}
	data := envelope.Data
	if len(data) > 0 && data[0] == '"' {
		var encoded string
		if err := json.Unmarshal(data, &encoded); err != nil {
			return geekNewsReply{}, fmt.Errorf("decode cyeam data string: %w", err)
		}
		data = []byte(encoded)
	}
	var outer struct {
		Date string          `json:"date"`
		News json.RawMessage `json:"news"`
	}
	if err := json.Unmarshal(data, &outer); err != nil {
		return geekNewsReply{}, fmt.Errorf("decode cyeam news data: %w", err)
	}
	var news geekNewsReply
	if err := json.Unmarshal(outer.News, &news); err != nil {
		return geekNewsReply{}, fmt.Errorf("decode cyeam news list: %w", err)
	}
	if news.CreateTime == 0 {
		news.CreateTime = firstNewsTime(news.News)
	}
	return news, nil
}

func firstNewsTime(items []geekNewsItem) int64 {
	if len(items) == 0 {
		return 0
	}
	return items[0].CreateTime
}

func (p *a2aPipeline) runGeekNews(ctx context.Context, turn a2a.ConversationTurn, profile a2aPromptProfileSpec, userText string) (string, error) {
	date, err := geekNewsDate(userText)
	if err != nil {
		return "", err
	}
	batch, err := p.newsFetcher.Fetch(ctx, date)
	if err != nil {
		return "", err
	}
	items := limitGeekNewsItems(batch.News)
	if len(items) < geekNewsMinimumItems {
		return "", fmt.Errorf("news source returned %d items, need at least %d", len(items), geekNewsMinimumItems)
	}

	for i := range items {
		translated, err := p.translateGeekNewsItem(ctx, turn, profile, i, items[i])
		if err != nil {
			logger.Infof("[A2A][geek-news][item_fallback] conversation_id=%s item=%d err=%v", turn.ConversationID, i, err)
			continue
		}
		items[i].Title = translated.Title
		items[i].Description = translated.Description
	}
	items = p.rankGeekNewsItems(ctx, turn, profile, items)
	result := geekNewsReply{
		CreateTime: batch.CreateTime,
		Summary:    fmt.Sprintf("%s 科技新闻精选，共 %d 条。", date, len(items)),
		News:       items,
	}
	return normalizeGeekNewsReply(mustMarshalGeekNews(result))
}

func geekNewsDate(userText string) (string, error) {
	var input struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal([]byte(userText), &input); err != nil || strings.TrimSpace(input.Date) == "" {
		return "", errors.New("geek-news input requires date")
	}
	return strings.TrimSpace(input.Date), nil
}

func limitGeekNewsItems(items []geekNewsItem) []geekNewsItem {
	filtered := make([]geekNewsItem, 0, min(len(items), geekNewsMaximumItems))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Link) == "" || strings.TrimSpace(item.Title) == "" {
			continue
		}
		if _, exists := seen[item.Link]; exists {
			continue
		}
		seen[item.Link] = struct{}{}
		filtered = append(filtered, item)
		if len(filtered) == geekNewsMaximumItems {
			break
		}
	}
	return filtered
}

type geekNewsTranslation struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (p *a2aPipeline) translateGeekNewsItem(ctx context.Context, turn a2a.ConversationTurn, profile a2aPromptProfileSpec, index int, item geekNewsItem) (geekNewsTranslation, error) {
	input, _ := json.Marshal(map[string]string{"title": item.Title, "description": item.Description})
	output := newGeekNewsItemStructuredOutput()
	_, err := p.agent.RunPromptProfile(ctx, agentruntime.PromptProfileRequest{
		Name:         "geek-news-item",
		SystemPrompt: "将一篇科技新闻翻译、润色为中文。只提交 title 和 description；保持事实、链接和专有名词，不要添加内容。",
		UserText:     string(input), ChannelName: turn.Channel, SessionKey: fmt.Sprintf("%s:item:%d", turn.ConversationID, index),
		DisableHistory: true, AllowTools: false, MaxSteps: 1, Model: profile.Model, StructuredOutput: output,
	})
	if err != nil {
		return geekNewsTranslation{}, err
	}
	raw, ok := output.Result()
	if !ok {
		return geekNewsTranslation{}, errors.New("translation did not submit structured output")
	}
	var translated geekNewsTranslation
	if err := json.Unmarshal([]byte(raw), &translated); err != nil || strings.TrimSpace(translated.Title) == "" || strings.TrimSpace(translated.Description) == "" {
		return geekNewsTranslation{}, errors.New("invalid translation output")
	}
	return translated, nil
}

func newGeekNewsItemStructuredOutput() *agentruntime.PromptProfileStructuredOutput {
	return agentruntime.NewPromptProfileStructuredOutput("structured_output", "提交这一篇新闻的中文标题和说明。", map[string]*schema.ParameterInfo{
		"title":       {Type: schema.String, Required: true},
		"description": {Type: schema.String, Required: true},
	}, func(value string) (string, error) {
		var translated geekNewsTranslation
		if err := json.Unmarshal([]byte(value), &translated); err != nil {
			return "", err
		}
		if strings.TrimSpace(translated.Title) == "" || strings.TrimSpace(translated.Description) == "" {
			return "", errors.New("title and description are required")
		}
		return jsonCompact(translated)
	})
}

func (p *a2aPipeline) rankGeekNewsItems(ctx context.Context, turn a2a.ConversationTurn, profile a2aPromptProfileSpec, items []geekNewsItem) []geekNewsItem {
	// Ranking is deliberately optional. A failure keeps the CLI's source order,
	// so a transient model error can never suppress the daily publication.
	candidates := make([]map[string]string, 0, len(items))
	for i, item := range items {
		candidates = append(candidates, map[string]string{
			"id": fmt.Sprintf("n%d", i), "title": item.Title, "description": truncateGeekNewsText(item.Description, 280),
		})
	}
	input, _ := json.Marshal(candidates)
	output := newGeekNewsOrderStructuredOutput()
	_, err := p.agent.RunPromptProfile(ctx, agentruntime.PromptProfileRequest{
		Name:         "geek-news-rank",
		SystemPrompt: "按科技新闻的重要性和时效性排序。只提交 ids，必须包含所有提供的 ID 且不重复。",
		UserText:     string(input), ChannelName: turn.Channel, SessionKey: turn.ConversationID + ":rank",
		DisableHistory: true, AllowTools: false, MaxSteps: 1, Model: profile.Model, StructuredOutput: output,
	})
	if err != nil {
		logger.Infof("[A2A][geek-news][rank_fallback] conversation_id=%s err=%v", turn.ConversationID, err)
		return items
	}
	raw, ok := output.Result()
	if !ok {
		logger.Infof("[A2A][geek-news][rank_fallback] conversation_id=%s err=no structured output", turn.ConversationID)
		return items
	}
	var order geekNewsOrder
	if err := json.Unmarshal([]byte(raw), &order); err != nil {
		return items
	}
	return applyGeekNewsOrder(items, order.IDs)
}

type geekNewsOrder struct {
	IDs []string `json:"ids"`
}

func newGeekNewsOrderStructuredOutput() *agentruntime.PromptProfileStructuredOutput {
	return agentruntime.NewPromptProfileStructuredOutput("structured_output", "提交所有新闻 ID 的最终顺序。", map[string]*schema.ParameterInfo{
		"ids": {Type: schema.Array, Required: true, ElemInfo: &schema.ParameterInfo{Type: schema.String}},
	}, func(value string) (string, error) {
		var order geekNewsOrder
		if err := json.Unmarshal([]byte(value), &order); err != nil || len(order.IDs) == 0 {
			if err == nil {
				err = errors.New("ids are required")
			}
			return "", err
		}
		return jsonCompact(order)
	})
}

func applyGeekNewsOrder(items []geekNewsItem, ids []string) []geekNewsItem {
	ordered := make([]geekNewsItem, 0, len(items))
	seen := make(map[int]bool, len(items))
	for _, id := range ids {
		var index int
		if _, err := fmt.Sscanf(id, "n%d", &index); err != nil || index < 0 || index >= len(items) || seen[index] {
			continue
		}
		seen[index] = true
		ordered = append(ordered, items[index])
	}
	for i := range items {
		if !seen[i] {
			ordered = append(ordered, items[i])
		}
	}
	return ordered
}

func truncateGeekNewsText(text string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "…"
}

func jsonCompact(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func mustMarshalGeekNews(value geekNewsReply) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}
