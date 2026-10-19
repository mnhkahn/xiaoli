package admin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"time"

	agentmedia "github.com/mnhkahn/xiaoli/internal/agent/media"
)

var errVisionImageTooLarge = errors.New("image too large")

// The camera remains first regardless of multipart ordering; screen content has its own label.
func (s *AdminServer) visionExplainImages(contentType string, body []byte) ([]agentmedia.VisionImage, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, errors.New("invalid content type")
	}
	if !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		image, ok := s.extractVisionImage(contentType, body)
		if !ok || len(image.Body) == 0 {
			return nil, errors.New("missing image")
		}
		if len(image.Body) > 2*1024*1024 {
			return nil, errVisionImageTooLarge
		}
		return []agentmedia.VisionImage{{Label: "相机照片", ContentType: image.ContentType, Body: image.Body}}, nil
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var photo, screenshot *agentmedia.VisionImage
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("invalid image multipart")
		}
		name := strings.ToLower(part.FormName())
		var target **agentmedia.VisionImage
		label := "相机照片"
		switch name {
		case "image", "photo", "picture", "file":
			target = &photo
		case "screenshot":
			target = &screenshot
			label = "平板当前屏幕截图"
		default:
			continue
		}
		if *target != nil {
			return nil, fmt.Errorf("duplicate %s", name)
		}
		payload, err := io.ReadAll(io.LimitReader(part, 2*1024*1024+1))
		if err != nil {
			return nil, errors.New("invalid image data")
		}
		if len(payload) > 2*1024*1024 {
			return nil, errVisionImageTooLarge
		}
		if len(payload) == 0 {
			return nil, errors.New("empty image")
		}
		*target = &agentmedia.VisionImage{Label: label, ContentType: normalizeImageContentType(part.Header.Get("Content-Type"), part.FileName()), Body: payload}
	}
	if photo == nil {
		return nil, errors.New("missing image")
	}
	images := []agentmedia.VisionImage{*photo}
	if screenshot != nil {
		images = append(images, *screenshot)
	}
	return images, nil
}

// Keep upload metadata on the server; tablets can continue returning text-only MCP results.
func (s *AdminServer) storeVisionCapture(deviceID, question, screenshotError string, images []agentmedia.VisionImage) {
	captureID := randomToken(16)
	// Camera last preserves existing latest-camera consumers.
	for i := len(images) - 1; i >= 0; i-- {
		image := images[i]
		id := s.storeVisionImage(deviceID, image.ContentType, image.Body)
		s.imagesMu.Lock()
		record := s.images[id]
		record.CaptureID = captureID
		record.Label = image.Label
		record.Question = question
		record.ScreenshotError = screenshotError
		s.images[id] = record
		s.imagesMu.Unlock()
	}
}

func (s *AdminServer) visionCapturePreview(deviceID string, since time.Time) ([]map[string]string, string, string) {
	records := s.visionCaptureRecords(deviceID, since)
	details := make([]map[string]string, 0, len(records))
	question, screenshotError := "", ""
	for _, record := range records {
		question, screenshotError = record.Question, record.ScreenshotError
		details = append(details, map[string]string{"url": "/admin/api/images/" + record.ID, "label": record.Label})
	}
	return details, question, screenshotError
}

// Read one capture as a batch so previews and notifications never mix different requests.
func (s *AdminServer) visionCaptureRecords(deviceID string, since time.Time) []imageRecord {
	s.imagesMu.Lock()
	defer s.imagesMu.Unlock()
	records := make([]imageRecord, 0, 2)
	captureID := ""
	ids := s.imagesByDev[deviceID]
	for i := len(ids) - 1; i >= 0; i-- {
		record, ok := s.images[ids[i]]
		if !ok {
			continue
		}
		if record.CreatedAt.Before(since) {
			break
		}
		if record.CaptureID == "" {
			continue
		}
		if captureID == "" {
			captureID = record.CaptureID
		}
		if record.CaptureID == captureID {
			records = append(records, record)
		}
	}
	return records
}
