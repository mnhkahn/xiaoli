package builtin

import (
	"context"
	"strings"
	"testing"
)

type fakeRecentImageStore struct {
	contentType string
	body        []byte
}

func (s fakeRecentImageStore) LatestImage(ctx context.Context, conversationID string) (string, []byte, bool) {
	if conversationID != "conv-1" {
		return "", nil, false
	}
	return s.contentType, append([]byte(nil), s.body...), true
}

type fakeVisionAnalyzer struct {
	question    string
	contentType string
	image       []byte
}

func (v *fakeVisionAnalyzer) Analyze(ctx context.Context, question string, contentType string, image []byte) (string, error) {
	v.question = question
	v.contentType = contentType
	v.image = append([]byte(nil), image...)
	return "左上角写着 hello。", nil
}

func TestInspectRecentImageToolUsesLatestConversationImage(t *testing.T) {
	vision := &fakeVisionAnalyzer{}
	tool := NewInspectRecentImageTool(vision, fakeRecentImageStore{
		contentType: "image/png",
		body:        []byte("png-bytes"),
	})
	ctx := WithRecentImageConversation(context.Background(), "conv-1")

	got, err := tool.InvokableRun(ctx, `{"question":"图里左上角写了什么？","image_ref":"latest"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if vision.question != "图里左上角写了什么？" || vision.contentType != "image/png" || string(vision.image) != "png-bytes" {
		t.Fatalf("vision call = question %q contentType %q image %q", vision.question, vision.contentType, string(vision.image))
	}
	if !strings.Contains(got, `"ok":true`) || !strings.Contains(got, "左上角写着 hello") {
		t.Fatalf("tool output = %s, want successful vision answer", got)
	}
}
