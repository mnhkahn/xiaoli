package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type RecentImageRecord struct {
	ID             string
	ConversationID string
	ContentType    string
	Body           []byte
	CreatedAt      time.Time
}

type RecentImageStore struct {
	mu                 sync.Mutex
	now                func() time.Time
	ttl                time.Duration
	maxPerConversation int
	byConversation     map[string][]RecentImageRecord
}

func NewRecentImageStore(now func() time.Time) *RecentImageStore {
	if now == nil {
		now = time.Now
	}
	return &RecentImageStore{
		now:                now,
		ttl:                2 * time.Hour,
		maxPerConversation: 5,
		byConversation:     map[string][]RecentImageRecord{},
	}
}

func (s *RecentImageStore) StoreImage(ctx context.Context, conversationID string, contentType string, body []byte) string {
	if s == nil || conversationID == "" || len(body) == 0 {
		return ""
	}
	record := RecentImageRecord{
		ID:             randomImageID(),
		ConversationID: conversationID,
		ContentType:    NormalizeImageContentType(contentType, ""),
		Body:           append([]byte(nil), body...),
		CreatedAt:      s.now(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(record.CreatedAt)
	records := append(s.byConversation[conversationID], record)
	if len(records) > s.maxPerConversation {
		records = records[len(records)-s.maxPerConversation:]
	}
	s.byConversation[conversationID] = records
	return record.ID
}

func (s *RecentImageStore) LatestImage(ctx context.Context, conversationID string) (string, []byte, bool) {
	if s == nil || conversationID == "" {
		return "", nil, false
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	records := s.byConversation[conversationID]
	if len(records) == 0 {
		return "", nil, false
	}
	record := records[len(records)-1]
	return record.ContentType, append([]byte(nil), record.Body...), true
}

func (s *RecentImageStore) pruneLocked(now time.Time) {
	if s.ttl <= 0 {
		return
	}
	for conversationID, records := range s.byConversation {
		keep := records[:0]
		for _, record := range records {
			if now.Sub(record.CreatedAt) <= s.ttl {
				keep = append(keep, record)
			}
		}
		if len(keep) == 0 {
			delete(s.byConversation, conversationID)
			continue
		}
		s.byConversation[conversationID] = keep
	}
}

func randomImageID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(b[:])
}
