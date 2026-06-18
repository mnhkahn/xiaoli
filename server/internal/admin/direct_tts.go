package admin

import (
	"net/http"
	"sync"
	"time"

	agentmedia "xiaoli/server/internal/agent/media"
)

type SpeechSynthesizer = agentmedia.SpeechSynthesizer

func newHTTPSpeechSynthesizer(cfg Config, client *http.Client) SpeechSynthesizer {
	return agentmedia.NewHTTPSpeechSynthesizer(agentmedia.TTSConfig{
		URL:            cfg.GoTTSURL,
		APIKey:         cfg.GoTTSAPIKey,
		Model:          cfg.GoTTSModel,
		Voice:          cfg.GoTTSVoice,
		ResponseFormat: cfg.GoTTSResponseFormat,
		Timeout:        cfg.GoTTSTimeout,
		HTTPClient:     client,
	})
}

type audioRecord struct {
	ID          string
	Token       string
	ContentType string
	Body        []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type audioStore struct {
	mu      sync.Mutex
	records map[string]audioRecord
	now     func() time.Time
	maxAge  time.Duration
}

func newAudioStore(now func() time.Time) *audioStore {
	if now == nil {
		now = time.Now
	}
	return &audioStore{
		records: map[string]audioRecord{},
		now:     now,
		maxAge:  10 * time.Minute,
	}
}

func (s *audioStore) put(contentType string, body []byte) audioRecord {
	now := s.now()
	record := audioRecord{
		ID:          randomToken(18),
		Token:       randomToken(18),
		ContentType: normalizeOggContentType(contentType),
		Body:        append([]byte(nil), body...),
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.maxAge),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, item := range s.records {
		if item.ExpiresAt.Before(now) {
			delete(s.records, id)
		}
	}
	s.records[record.ID] = record
	return record
}

func (s *audioStore) get(id string, token string) (audioRecord, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok || record.Token == "" || record.Token != token || record.ExpiresAt.Before(now) {
		return audioRecord{}, false
	}
	return record, true
}

func normalizeOggContentType(value string) string {
	return agentmedia.NormalizeOggContentType(value)
}
