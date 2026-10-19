package admin

import (
	"bytes"
	"context"
	"encoding/json"
	agentmedia "github.com/mnhkahn/xiaoli/internal/agent/media"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type pairedVisionAnalyzer struct {
	images   []agentmedia.VisionImage
	question string
}

func (f *pairedVisionAnalyzer) Analyze(context.Context, string, string, []byte) (string, error) {
	panic("expected multi image analysis")
}
func (f *pairedVisionAnalyzer) AnalyzeImages(_ context.Context, question string, images []agentmedia.VisionImage) (string, error) {
	f.images = images
	f.question = question
	return "屏幕在聊天，相机里正在学习。", nil
}

func visionPairBody(t *testing.T, fields ...string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range fields {
		part, err := writer.CreateFormFile(field, field+".jpg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte(field + "-bytes")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.WriteField("question", "正在做什么？"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func TestVisionExplainSendsCameraAndScreenTogether(t *testing.T) {
	cfg := testConfig()
	cfg.DirectDeviceServer = true
	cfg.DeviceAuthEnabled = false
	srv := NewServer(cfg)
	analyzer := &pairedVisionAnalyzer{}
	srv.deviceHub.vision = analyzer
	// The order on the wire must not swap the image labels.
	body, contentType := visionPairBody(t, "screenshot", "image")
	req := httptest.NewRequest(http.MethodPost, "/mcp/vision/explain", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Device-Id", "device-1")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	if len(analyzer.images) != 2 || string(analyzer.images[0].Body) != "image-bytes" || string(analyzer.images[1].Body) != "screenshot-bytes" {
		t.Fatalf("images=%+v", analyzer.images)
	}
	if analyzer.images[0].Label != "相机照片" || analyzer.images[1].Label != "平板当前屏幕截图" {
		t.Fatalf("labels=%+v", analyzer.images)
	}
	if !strings.Contains(analyzer.question, "正在做什么？") {
		t.Fatalf("question=%s", analyzer.question)
	}
	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["response"] != "屏幕在聊天，相机里正在学习。" {
		t.Fatal(response)
	}
}

func TestVisionExplainRejectsAmbiguousOrOversizedImages(t *testing.T) {
	srv := &AdminServer{}
	for _, fields := range [][]string{{"image", "image"}, {"image", "screenshot", "screenshot"}, {"screenshot"}} {
		body, contentType := visionPairBody(t, fields...)
		if _, err := srv.visionExplainImages(contentType, body); err == nil {
			t.Fatalf("accepted %v", fields)
		}
	}
	if _, err := srv.visionExplainImages("image/jpeg", make([]byte, 2*1024*1024+1)); err == nil {
		t.Fatal("accepted oversized image")
	}
}

func TestVisionExplainDoesNotSilentlyDropScreen(t *testing.T) {
	cfg := testConfig()
	cfg.DirectDeviceServer = true
	cfg.DeviceAuthEnabled = false
	srv := NewServer(cfg)
	srv.deviceHub.vision = fakeVisionAnalyzer{answer: "single only"}
	body, contentType := visionPairBody(t, "image", "screenshot")
	req := httptest.NewRequest(http.MethodPost, "/mcp/vision/explain", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Device-Id", "device-1")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
}
