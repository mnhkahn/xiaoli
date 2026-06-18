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
