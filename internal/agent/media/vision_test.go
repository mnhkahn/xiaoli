package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIVisionIncludesBothLabeledImagesInOneRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if len(payload.Messages) != 2 {
			t.Errorf("messages=%d", len(payload.Messages))
			return
		}
		var content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if err := json.Unmarshal(payload.Messages[1].Content, &content); err != nil {
			t.Error(err)
			return
		}
		if len(content) != 5 {
			t.Errorf("content=%s", payload.Messages[1].Content)
			return
		}
		if content[1].Text != "相机照片" || content[3].Text != "平板当前屏幕截图" {
			t.Errorf("content=%s", payload.Messages[1].Content)
		}
		if !strings.HasSuffix(content[2].ImageURL.URL, ";base64,cGhvdG8=") || !strings.HasSuffix(content[4].ImageURL.URL, ";base64,c2NyZWVu") {
			t.Errorf("images=%s", payload.Messages[1].Content)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"分析结果"}}]}`))
	}))
	defer server.Close()
	client := NewOpenAIVisionClient(VisionConfig{URL: server.URL, APIKey: "test-key", Model: "test", Timeout: time.Second}).(MultiImageVisionAnalyzer)
	answer, err := client.AnalyzeImages(context.Background(), "在做什么？", []VisionImage{{Label: "相机照片", ContentType: "image/jpeg", Body: []byte("photo")}, {Label: "平板当前屏幕截图", ContentType: "image/jpeg", Body: []byte("screen")}})
	if err != nil || answer != "分析结果" || calls != 1 {
		t.Fatalf("answer=%s err=%v calls=%d", answer, err, calls)
	}
}
