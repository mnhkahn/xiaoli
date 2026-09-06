package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
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
	geekNewsBatchMaxItems       = 3
	geekNewsBatchMaxInputRunes  = 8000
	geekNewsBatchMaxTitleRunes  = 1000
	// Leave two minutes of the 20-minute A2A HTTP budget to stop work,
	// serialize the partial result, and send a successful response.
	geekNewsProcessingTimeout = 18 * time.Minute
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
	processingCtx, cancel := context.WithTimeout(ctx, geekNewsProcessingTimeout)
	defer cancel()

	batch, err := p.newsFetcher.Fetch(processingCtx, date)
	if err != nil {
		return "", err
	}
	// Keep the two tabs independent, while applying the same ranking policy to
	// each one. This preserves their product-level grouping without inheriting
	// the upstream CLI's arbitrary source order.
	news := p.processGeekNewsItems(processingCtx, turn, profile, "news", batch.News)
	if processingCtx.Err() == nil && len(news) > 1 {
		news = p.rankGeekNewsItems(processingCtx, turn, profile, news)
	}
	aiNews := p.processGeekNewsItems(processingCtx, turn, profile, "ai_news", batch.AINews)
	if processingCtx.Err() == nil && len(aiNews) > 1 {
		aiNews = p.rankGeekNewsItems(processingCtx, turn, profile, aiNews)
	}
	if errors.Is(processingCtx.Err(), context.DeadlineExceeded) {
		logger.Infof("[A2A][geek-news][processing_deadline] conversation_id=%s date=%s fallback=accepted_items", turn.ConversationID, date)
	}
	result := geekNewsReply{
		CreateTime: batch.CreateTime,
		Summary:    fmt.Sprintf("%s 科技新闻：技术动向 %d 条，AI 资讯 %d 条。", date, len(news), len(aiNews)),
		News:       news,
		AINews:     aiNews,
	}
	if len(result.News)+len(result.AINews) == 0 {
		return "", errors.New("geek-news: no items passed validation")
	}
	if err := validateGeekNewsDelivery(result); err != nil {
		logger.Infof("[A2A][geek-news][acceptance_failed] conversation_id=%s date=%s err=%v", turn.ConversationID, date, err)
		return "", fmt.Errorf("geek-news acceptance failed: %w", err)
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

	accepted := make([]geekNewsItem, 0, len(items))
	for _, indexes := range geekNewsBatchIndexes(processed) {
		if ctx.Err() != nil {
			break
		}
		chunk := make([]geekNewsItem, len(indexes))
		for i, index := range indexes {
			chunk[i] = processed[index]
		}
		translated, _, err := p.translateGeekNewsBatch(ctx, turn, profile, group, chunk)
		if err != nil {
			logger.Infof("[A2A][geek-news][batch_translation_fallback] conversation_id=%s group=%s items=%d err=%v", turn.ConversationID, group, len(chunk), err)
		} else {
			chunk = translated
		}
		// Validate and repair this batch's items before starting the next batch.
		// Only accepted items enter the delivery list; a failed sibling cannot
		// invalidate them or cause them to be generated again.
		for i, item := range chunk {
			index := indexes[i]
			if !containsChinese(item.Title) && ctx.Err() == nil {
				if title, err := p.translateGeekNewsTitle(ctx, turn, profile, group, index, item.SourceTitle); err == nil {
					item.Title = title
				}
			}
			if containsChinese(item.Title) && !geekNewsDescriptionValid(item.Description) && ctx.Err() == nil {
				if description, err := p.translateGeekNewsDescription(ctx, turn, profile, group, index, item); err == nil {
					item.Description = description
				}
			}
			if !containsChinese(item.Title) || !geekNewsDescriptionValid(item.Description) {
				logger.Infof("[A2A][geek-news][item_rejected] conversation_id=%s group=%s item=%d chinese_chars=%d ctx_err=%v", turn.ConversationID, group, index, countChineseRunes(item.Description), ctx.Err())
				continue
			}
			accepted = append(accepted, item)
		}
	}
	return accepted
}

func geekNewsBatchIndexes(items []geekNewsItem) [][]int {
	batches := make([][]int, 0, (len(items)+geekNewsBatchMaxItems-1)/geekNewsBatchMaxItems)
	current := make([]int, 0, geekNewsBatchMaxItems)
	currentRunes := 0
	for index, item := range items {
		input := geekNewsTranslationInput(item.SourceTitle, item.Description)
		itemRunes := len([]rune(input.Title)) + len([]rune(input.Description))
		if len(current) > 0 && (len(current) == geekNewsBatchMaxItems || currentRunes+itemRunes > geekNewsBatchMaxInputRunes) {
			batches = append(batches, current)
			current = make([]int, 0, geekNewsBatchMaxItems)
			currentRunes = 0
		}
		current = append(current, index)
		currentRunes += itemRunes
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

type geekNewsBatchTranslation struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type geekNewsBatchTranslations struct {
	Items []geekNewsBatchTranslation `json:"items"`
}

type geekNewsTranslationInputItem struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func geekNewsTranslationInput(title, description string) geekNewsTranslationInputItem {
	title = truncateGeekNewsInput(title, geekNewsBatchMaxTitleRunes)
	descriptionBudget := geekNewsBatchMaxInputRunes - len([]rune(title))
	return geekNewsTranslationInputItem{
		Title:       title,
		Description: truncateGeekNewsInput(description, descriptionBudget),
	}
}

func truncateGeekNewsInput(text string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(text))
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return string(runes)
	}
	marker := "\n[原文过长，以下仅保留开头和结尾]\n"
	markerRunes := len([]rune(marker))
	if maxRunes <= markerRunes {
		return string(runes[:maxRunes])
	}
	head := (maxRunes - markerRunes) / 2
	tail := maxRunes - markerRunes - head
	return string(runes[:head]) + marker + string(runes[len(runes)-tail:])
}

func (p *a2aPipeline) translateGeekNewsBatch(ctx context.Context, turn a2a.ConversationTurn, profile a2aPromptProfileSpec, group string, items []geekNewsItem) ([]geekNewsItem, []int, error) {
	inputItems := make([]map[string]any, 0, len(items))
	for index, item := range items {
		translationInput := geekNewsTranslationInput(item.SourceTitle, item.Description)
		inputItems = append(inputItems, map[string]any{"id": index, "title": translationInput.Title, "description": translationInput.Description})
	}
	input, err := json.Marshal(map[string]any{"items": inputItems})
	if err != nil {
		return nil, nil, err
	}
	output := newGeekNewsBatchStructuredOutput()
	_, err = p.agent.RunPromptProfile(ctx, agentruntime.PromptProfileRequest{
		Name: "geek-news-batch-translation",
		SystemPrompt: "任务目标：将一组科技新闻的标题和 description 翻译、润色成可直接发布的中文内容。\n" +
			"验收标准（必须逐条全部满足后才能调用 structured_output 提交）：\n" +
			"1. 每个 id 必须原样保留且只出现一次；\n" +
			"2. title 使用中文，保持原有新闻事实；\n" +
			"3. description 使用自然中文，保持事实、专有名词和原意，不编造，并完整说明新闻背景、核心事实、关键细节和潜在影响；\n" +
			"4. 每条 description 的汉字数必须为 300–500；\n" +
			"5. 若原文带有“仅保留开头和结尾”标记，只能基于所给摘录写作，不得补造摘录中未出现的事实；\n" +
			"6. 只提交 items；每项只包含 id、title、description。\n" +
			"执行方式：先完成全部翻译，再逐项自检；字数不足或其他标准不满足时继续修改，全部验收通过后才调用 structured_output。",
		UserText: string(input), ChannelName: turn.Channel, SessionKey: turn.ConversationID + ":" + group + ":batch-translation",
		DisableHistory: true, AllowTools: false, MaxSteps: 2, Model: profile.Model, StructuredOutput: output,
	})
	// Keep captured results even if the runner fails after submitting them.
	raw, ok := output.Result()
	if !ok {
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New("batch translation did not submit structured output")
	}
	var translated geekNewsBatchTranslations
	if err := json.Unmarshal([]byte(raw), &translated); err != nil {
		return nil, nil, err
	}
	processed := append([]geekNewsItem(nil), items...)
	byID := make(map[int]geekNewsBatchTranslation, len(translated.Items))
	for _, item := range translated.Items {
		if item.ID < 0 || item.ID >= len(processed) {
			return nil, nil, fmt.Errorf("batch translation returned unknown item id %d", item.ID)
		}
		if _, exists := byID[item.ID]; exists {
			return nil, nil, fmt.Errorf("batch translation returned duplicate item id %d", item.ID)
		}
		byID[item.ID] = item
	}
	failed := make([]int, 0)
	for index := range processed {
		item, ok := byID[index]
		if !ok {
			failed = append(failed, index)
			continue
		}
		accepted := true
		if containsChinese(item.Title) {
			processed[index].Title = item.Title
		} else {
			accepted = false
		}
		processed[index].Description = item.Description
		if !geekNewsDescriptionValid(item.Description) {
			accepted = false
		}
		if !accepted {
			failed = append(failed, index)
		}
	}
	return processed, failed, nil
}

func newGeekNewsBatchStructuredOutput() *agentruntime.PromptProfileStructuredOutput {
	item := &schema.ParameterInfo{Type: schema.Object, SubParams: map[string]*schema.ParameterInfo{
		"id":          {Type: schema.Integer, Required: true},
		"title":       {Type: schema.String, Required: true},
		"description": {Type: schema.String, Required: true},
	}}
	return agentruntime.NewPromptProfileStructuredOutput("structured_output", "提交这一组新闻的中文标题和说明。", map[string]*schema.ParameterInfo{
		"items": {Type: schema.Array, Required: true, ElemInfo: item},
	}, func(value string) (string, error) {
		var translated geekNewsBatchTranslations
		if err := json.Unmarshal([]byte(value), &translated); err != nil {
			return "", err
		}
		return jsonCompact(translated)
	})
}

func (p *a2aPipeline) translateGeekNewsDescription(ctx context.Context, turn a2a.ConversationTurn, profile a2aPromptProfileSpec, group string, index int, item geekNewsItem) (string, error) {
	translationInput := geekNewsTranslationInput(item.SourceTitle, item.Description)
	var lastErr error
	previous := item.Description
	for attempt := 1; attempt <= geekNewsDescriptionAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		feedback := fmt.Sprintf("description must contain %d-%d Chinese characters; previous output contains %d", geekNewsDescriptionMinRunes, geekNewsDescriptionMaxRunes, countChineseRunes(previous))
		if lastErr != nil {
			feedback += "; " + lastErr.Error()
		}
		input, _ := json.Marshal(map[string]any{"source": translationInput, "previous_description": previous, "validation_error": feedback})
		output := newGeekNewsDescriptionStructuredOutput()
		_, err := p.agent.RunPromptProfile(ctx, agentruntime.PromptProfileRequest{
			Name: "geek-news-description",
			SystemPrompt: "任务目标：把一篇科技新闻的 description 翻译、润色成可直接发布的中文说明。\n" +
				"验收标准（必须全部满足后才能调用 structured_output 提交）：\n" +
				"1. 保持事实、专有名词和原意，不编造；\n" +
				"2. 使用自然中文完整说明新闻背景、核心事实、关键细节和潜在影响；\n" +
				"3. description 的汉字数必须为 300–500；\n" +
				"4. 只提交 description，不要提交标题、链接或其他字段。\n" +
				"执行方式：先完成翻译，再逐项自检；任一标准不满足时继续修改，验收通过后才调用 structured_output。",
			UserText: string(input), ChannelName: turn.Channel, SessionKey: fmt.Sprintf("%s:%s:item:%d:description:%d", turn.ConversationID, group, index, attempt),
			// Structured output consumes one model step and one tool-result step.
			DisableHistory: true, AllowTools: false, MaxSteps: 2, Model: profile.Model, StructuredOutput: output,
		})
		if err != nil {
			lastErr = err
			if raw, _, ok := output.Failure(); ok {
				var failed geekNewsDescription
				if json.Unmarshal([]byte(raw), &failed) == nil {
					previous = failed.Description
				}
			}
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
		Name: "geek-news-title", SystemPrompt: "任务目标：将给定的纯英文新闻标题翻译为中文标题。\n验收标准：保留原有新闻事实；标题必须包含中文；只提交 title，不得输出链接、日期、描述或任何其他字段。\n执行方式：先翻译并自检，验收通过后才调用 structured_output。",
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
	chineseCount := countChineseRunes(description)
	return chineseCount >= geekNewsDescriptionMinRunes && chineseCount <= geekNewsDescriptionMaxRunes
}

func countChineseRunes(text string) int {
	count := 0
	for _, r := range strings.TrimSpace(text) {
		if unicode.Is(unicode.Han, r) {
			count++
		}
	}
	return count
}

func containsChinese(text string) bool {
	return countChineseRunes(text) > 0
}

// validateGeekNewsDelivery checks delivery invariants for already accepted items.
// Failed source items are excluded during processing, not rejected here as a batch.
func validateGeekNewsDelivery(reply geekNewsReply) error {
	if !containsChinese(reply.Summary) {
		return errors.New("summary must contain Chinese")
	}
	for _, group := range []struct {
		name  string
		items []geekNewsItem
	}{
		{name: "news", items: reply.News},
		{name: "ai_news", items: reply.AINews},
	} {
		for index, item := range group.items {
			if !containsChinese(item.Title) {
				return fmt.Errorf("%s item %d title must contain Chinese", group.name, index)
			}
			if !geekNewsDescriptionValid(item.Description) {
				return fmt.Errorf("%s item %d description must contain %d-%d Chinese characters", group.name, index, geekNewsDescriptionMinRunes, geekNewsDescriptionMaxRunes)
			}
		}
	}
	return nil
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
