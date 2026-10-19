package media

import "context"

type SpeechRecognizer interface {
	Transcribe(ctx context.Context, oggOpus []byte) (string, error)
}

type SpeechSynthesizer interface {
	Synthesize(ctx context.Context, text string) (contentType string, body []byte, err error)
}

type VisionAnalyzer interface {
	Analyze(ctx context.Context, question string, contentType string, image []byte) (string, error)
}

// VisionImage labels each image so the model can distinguish camera and screen content.
type VisionImage struct {
	Label       string
	ContentType string
	Body        []byte
}

// MultiImageVisionAnalyzer is optional to preserve compatibility with single-image adapters.
type MultiImageVisionAnalyzer interface {
	AnalyzeImages(ctx context.Context, question string, images []VisionImage) (string, error)
}
