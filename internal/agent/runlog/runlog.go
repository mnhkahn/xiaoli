package runlog

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	agentevent "github.com/mnhkahn/xiaoli-esp32/internal/event"
)

type Writer struct {
	mu  sync.Mutex
	dir string
}

func NewWriter(dir string) *Writer {
	return &Writer{dir: dir}
}

func Subscribe(bus agentevent.Subscriber, dir string) agentevent.UnsubscribeFunc {
	if bus == nil {
		return func() {}
	}
	return bus.SubscribeAll(NewWriter(dir).Handle)
}

func ReadSession(dir, sessionID string, limit int) ([]agentevent.Event, error) {
	if strings.TrimSpace(sessionID) == "" {
		sessionID = "default"
	}
	path := filepath.Join(dir, safeFileID(sessionID)+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var events []agentevent.Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e agentevent.Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return nil, err
		}
		events = append(events, e)
		if limit > 0 && len(events) > limit {
			events = events[len(events)-limit:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (w *Writer) Handle(_ context.Context, e agentevent.Event) error {
	if w == nil || strings.TrimSpace(w.dir) == "" {
		return nil
	}
	sessionID := strings.TrimSpace(e.SessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	path := filepath.Join(w.dir, safeFileID(sessionID)+".jsonl")
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func safeFileID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "default"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(id)
}
