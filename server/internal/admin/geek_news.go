package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"unicode"

	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
	agentruntime "github.com/mnhkahn/xiaoli/internal/agent/runtime"
	a2a "github.com/mnhkahn/xiaoli/server/internal/a2a"
)

const (
	geekNewsDescriptionMinRunes = 300
	geekNewsDescriptionMaxRunes = 500
	geekNewsDescriptionAttempts = 2
	geekNewsConcurrentWorkers   = 4
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
		Date   string          `json:"date"`
		News   json.RawMessage `json:"news"`
		AINews json.RawMessage `json:"ai_news"`
	}
	if err := json.Unmarshal(data, &outer); err != nil {
		return geekNewsReply{}, fmt.Errorf("decode cyeam news data: %w", err)
	}
	news, err := decodeCYEAMGeekNewsGroup(outer.News)
	if err != nil {
		return geekNewsReply{}, fmt.Errorf("decode cyeam news list: %w", err)
	}
	aiNews, err := decodeCYEAMGeekNewsGroup(outer.AINews)
	if err != nil {
		return geekNewsReply{}, fmt.Errorf("decode cyeam ai news list: %w", err)
	}
	news.AINews = aiNews.News
	if news.CreateTime == 0 {
		news.CreateTime = firstNewsTime(news.News)
		if news.CreateTime == 0 {
			news.CreateTime = firstNewsTime(news.AINews)
		}
	}
	return news, nil
}

func decodeCYEAMGeekNewsGroup(raw json.RawMessage) (geekNewsReply, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return geekNewsReply{News: []geekNewsItem{}}, nil
	}
	var group geekNewsReply
	if err := json.Unmarshal(raw, &group); err == nil && group.News != nil {
		return group, nil
	}
	var items []geekNewsItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return geekNewsReply{}, err
	}
	return geekNewsReply{News: items}, nil
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
	// Keep the two tabs independent, while applying the same ranking policy to
	// each one. This preserves their product-level grouping without inheriting
	// the upstream CLI's arbitrary source order.
	news := p.rankGeekNewsItems(ctx, turn, profile, p.processGeekNewsItems(ctx, turn, profile, "news", batch.News))
	aiNews := p.rankGeekNewsItems(ctx, turn, profile, p.processGeekNewsItems(ctx, turn, profile, "ai_news", batch.AINews))
	result := geekNewsReply{
		CreateTime: batch.CreateTime,
		Summary:    fmt.Sprintf("%s 科技新闻：技术动向 %d 条，AI 资讯 %d 条。", date, len(news), len(aiNews)),
		News:       news,
		AINews:     aiNews,
	}
	reply, err := normalizeGeekNewsReply(mustMarshalGeekNews(result))
	if err != nil {
		return "", err
	}
	logger.Infof("[A2A][geek-news][completed] conversation_id=%s date=%s news=%d ai_news=%d response_schema=create_time,summary,news,ai_news", turn.ConversationID, date, len(result.News), len(result.AINews))
	return reply, nil
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

type geekNewsDescription struct {
	Description string `json:"description"`
}

func (p *a2aPipeline) processGeekNewsItems(ctx context.Context, turn a2a.ConversationTurn, profile a2aPromptProfileSpec, group string, items []geekNewsItem) []geekNewsItem {
	processed := make([]geekNewsItem, len(items))
	for i := range items {
		processed[i] = items[i]
		processed[i].SourceTitle = items[i].Title
	}

	// Each output slot belongs to exactly one worker, so concurrent model calls
	// cannot drop, reorder, or merge source items.
	workers := make(chan struct{}, geekNewsConcurrentWorkers)
	var wg sync.WaitGroup
	for i := range processed {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			select {
			case workers <- struct{}{}:
				defer func() { <-workers }()
			case <-ctx.Done():
				return
			}

			item := processed[index]
			if isPureEnglishTitle(item.SourceTitle) {
				if title, err := p.translateGeekNewsTitle(ctx, turn, profile, group, index, item.SourceTitle); err == nil {
					item.Title = title
				} else {
					logger.Infof("[A2A][geek-news][title_source_fallback] conversation_id=%s group=%s item=%d err=%v", turn.ConversationID, group, index, err)
				}
			}
			if description, err := p.translateGeekNewsDescription(ctx, turn, profile, group, index, item); err == nil {
				item.Description = description
			} else {
				logger.Infof("[A2A][geek-news][description_source_fallback] conversation_id=%s group=%s item=%d err=%v", turn.ConversationID, group, index, err)
			}
			processed[index] = item
		}(i)
	}
	wg.Wait()
	return processed
}

func (p *a2aPipeline) translateGeekNewsDescription(ctx context.Context, turn a2a.ConversationTurn, profile a2aPromptProfileSpec, group string, index int, item geekNewsItem) (string, error) {
	input, _ := json.Marshal(map[string]string{"title": item.SourceTitle, "description": item.Description})
	var lastErr error
	for attempt := 1; attempt <= geekNewsDescriptionAttempts; attempt++ {
		output := newGeekNewsDescriptionStructuredOutput()
		_, err := p.agent.RunPromptProfile(ctx, agentruntime.PromptProfileRequest{
			Name:         "geek-news-description",
			SystemPrompt: "将一篇科技新闻的 description 翻译、润色为中文。只提交 description；保持事实、专有名词和原意，不要编造。description 必须为 300–500 个中文字符，完整说明新闻背景、核心事实、关键细节和潜在影响。",
			UserText:     string(input), ChannelName: turn.Channel, SessionKey: fmt.Sprintf("%s:%s:item:%d:description:%d", turn.ConversationID, group, index, attempt),
			// Structured output consumes one model step and one tool-result step.
			DisableHistory: true, AllowTools: false, MaxSteps: 2, Model: profile.Model, StructuredOutput: output,
		})
		if err != nil {
			lastErr = err
			continue
		}
		raw, ok := output.Result()
		if !ok {
			lastErr = errors.New("description translation did not submit structured output")
			continue
		}
		var translated geekNewsDescription
		if err := json.Unmarshal([]byte(raw), &translated); err != nil || !geekNewsDescriptionValid(translated.Description) {
			lastErr = errors.New("invalid description translation output")
			continue
		}
		return translated.Description, nil
	}
	return "", fmt.Errorf("translate description after %d attempts: %w", geekNewsDescriptionAttempts, lastErr)
}

func newGeekNewsDescriptionStructuredOutput() *agentruntime.PromptProfileStructuredOutput {
	return agentruntime.NewPromptProfileStructuredOutput("structured_output", "提交这一篇新闻的中文说明。", map[string]*schema.ParameterInfo{
		"description": {Type: schema.String, Required: true},
	}, func(value string) (string, error) {
		var translated geekNewsDescription
		if err := json.Unmarshal([]byte(value), &translated); err != nil {
			return "", err
		}
		if !geekNewsDescriptionValid(translated.Description) {
			return "", fmt.Errorf("description must contain %d-%d characters", geekNewsDescriptionMinRunes, geekNewsDescriptionMaxRunes)
		}
		return jsonCompact(translated)
	})
}

func (p *a2aPipeline) translateGeekNewsTitle(ctx context.Context, turn a2a.ConversationTurn, profile a2aPromptProfileSpec, group string, index int, sourceTitle string) (string, error) {
	output := newGeekNewsTitleStructuredOutput()
	_, err := p.agent.RunPromptProfile(ctx, agentruntime.PromptProfileRequest{
		Name: "geek-news-title", SystemPrompt: "将给定的纯英文新闻标题翻译为中文。只提交 title；不得添加或改写新闻事实，不得输出链接、日期、描述或任何其他字段。",
		UserText: sourceTitle, ChannelName: turn.Channel, SessionKey: fmt.Sprintf("%s:%s:item:%d:title", turn.ConversationID, group, index),
		DisableHistory: true, AllowTools: false, MaxSteps: 2, Model: profile.Model, StructuredOutput: output,
	})
	if err != nil {
		return "", err
	}
	raw, ok := output.Result()
	if !ok {
		return "", errors.New("title translation did not submit structured output")
	}
	var translated struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(raw), &translated); err != nil || strings.TrimSpace(translated.Title) == "" {
		return "", errors.New("invalid title translation output")
	}
	return translated.Title, nil
}

func newGeekNewsTitleStructuredOutput() *agentruntime.PromptProfileStructuredOutput {
	return agentruntime.NewPromptProfileStructuredOutput("structured_output", "提交这一篇新闻的中文标题。", map[string]*schema.ParameterInfo{
		"title": {Type: schema.String, Required: true},
	}, func(value string) (string, error) {
		var translated struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal([]byte(value), &translated); err != nil || strings.TrimSpace(translated.Title) == "" {
			if err == nil {
				err = errors.New("title is required")
			}
			return "", err
		}
		return jsonCompact(translated)
	})
}

func isPureEnglishTitle(title string) bool {
	hasLatin := false
	for _, r := range strings.TrimSpace(title) {
		switch {
		case unicode.IsLetter(r):
			if !unicode.Is(unicode.Latin, r) {
				return false
			}
			hasLatin = true
		case unicode.IsDigit(r) || unicode.IsSpace(r) || unicode.IsPunct(r):
		default:
			return false
		}
	}
	return hasLatin
}

func geekNewsDescriptionValid(description string) bool {
	length := len([]rune(strings.TrimSpace(description)))
	return length >= geekNewsDescriptionMinRunes && length <= geekNewsDescriptionMaxRunes
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
		// Structured output consumes one model step and one tool-result step.
		DisableHistory: true, AllowTools: false, MaxSteps: 2, Model: profile.Model, StructuredOutput: output,
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
