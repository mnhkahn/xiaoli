package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type VisionAnalyzer interface {
	Analyze(ctx context.Context, question string, contentType string, image []byte) (string, error)
}

type RecentImageStore interface {
	LatestImage(ctx context.Context, conversationID string) (contentType string, body []byte, ok bool)
}

type InspectRecentImageTool struct {
	vision VisionAnalyzer
	store  RecentImageStore
}

func NewInspectRecentImageTool(vision VisionAnalyzer, store RecentImageStore) tool.InvokableTool {
	return &InspectRecentImageTool{vision: vision, store: store}
}

func (t *InspectRecentImageTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "inspect_recent_image",
		Desc: `Inspect the latest image sent in the current conversation.

Use this when the user asks a follow-up question about an image they recently sent, such as OCR, details, objects, layout, or visual content. image_ref currently supports only "latest".`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"question": {
				Type:     schema.String,
				Desc:     "Specific question to ask about the recent image.",
				Required: true,
			},
			"image_ref": {
				Type: schema.String,
				Desc: "Image reference. Use \"latest\" for the most recent image in this conversation.",
				Enum: []string{"latest"},
			},
		}),
	}, nil
}

func (t *InspectRecentImageTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args struct {
		Question string `json:"question"`
		ImageRef string `json:"image_ref"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return errorResponse("invalid JSON arguments: %v", err), nil
	}
	args.Question = strings.TrimSpace(args.Question)
	if args.Question == "" {
		return errorResponse("question is required"), nil
	}
	if args.ImageRef != "" && args.ImageRef != "latest" {
		return errorResponse("image_ref must be \"latest\""), nil
	}
	if t.vision == nil {
		return errorResponse("vision analyzer is not configured"), nil
	}
	if t.store == nil {
		return errorResponse("recent image store is not configured"), nil
	}
	conversationID := recentImageConversation(ctx)
	if conversationID == "" {
		return errorResponse("current conversation is unknown"), nil
	}
	contentType, body, ok := t.store.LatestImage(ctx, conversationID)
	if !ok {
		return errorResponse("no recent image found for this conversation"), nil
	}
	answer, err := t.vision.Analyze(ctx, args.Question, contentType, body)
	if err != nil {
		return errorResponse("vision analysis failed: %v", err), nil
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return errorResponse("vision analysis returned empty answer"), nil
	}
	raw, _ := json.Marshal(map[string]any{
		"ok":        true,
		"image_ref": "latest",
		"answer":    answer,
	})
	return string(raw), nil
}

func inspectRecentImageToolAvailable(vision VisionAnalyzer, store RecentImageStore) bool {
	return vision != nil && store != nil
}

func missingInspectRecentImageConfig() error {
	return fmt.Errorf("vision analyzer and recent image store are required")
}
