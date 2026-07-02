package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/mnhkahn/gogogo/logger"
)

type localState struct {
	Sessions map[string]Info   `json:"sessions"`
	Channels map[string]string `json:"channels"`
	Epochs   map[string]string `json:"epochs"`
}

type LocalManager struct {
	mu    sync.Mutex
	path  string
	state localState
}

func NewLocalManager(dataDir string) *LocalManager {
	m := &LocalManager{path: filepath.Join(dataDir, "state", "sessions.json")}
	m.state = localState{
		Sessions: map[string]Info{},
		Channels: map[string]string{},
		Epochs:   map[string]string{},
	}
	m.load()
	return m
}

func (m *LocalManager) load() {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Infof("local session load failed: %v", err)
		}
		return
	}
	if err := json.Unmarshal(data, &m.state); err != nil {
		logger.Infof("local session parse failed: %v", err)
	}
	if m.state.Sessions == nil {
		m.state.Sessions = map[string]Info{}
	}
	if m.state.Channels == nil {
		m.state.Channels = map[string]string{}
	}
	if m.state.Epochs == nil {
		m.state.Epochs = map[string]string{}
	}
}

func (m *LocalManager) save() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *LocalManager) channelKey(channelName, channelUser string) string {
	return enc(channelName) + ":" + enc(channelUser)
}

func (m *LocalManager) GetOrCreate(ctx context.Context, channelName, channelUser, model string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sid := m.state.Channels[m.channelKey(channelName, channelUser)]; sid != "" {
		if _, ok := m.state.Sessions[sid]; ok {
			return sid, false, nil
		}
	}
	return m.createLocked(ctx, channelName, channelUser, model)
}

func (m *LocalManager) Create(ctx context.Context, channelName, channelUser, model string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createLocked(ctx, channelName, channelUser, model)
}

func (m *LocalManager) createLocked(_ context.Context, channelName, channelUser, model string) (string, bool, error) {
	sessionID := randomID()
	now := time.Now().Format(time.RFC3339)
	m.state.Sessions[sessionID] = Info{
		ID:          sessionID,
		ChannelName: channelName,
		ChannelUser: channelUser,
		Title:       "新会话",
		Model:       model,
		CreatedAt:   now,
		UpdatedAt:   now,
		Count:       0,
	}
	m.state.Channels[m.channelKey(channelName, channelUser)] = sessionID
	if err := m.save(); err != nil {
		return "", false, err
	}
	return sessionID, true, nil
}

func (m *LocalManager) SetTitle(_ context.Context, sessionID, title string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	runes := []rune(title)
	if len(runes) > 20 {
		title = string(runes[:20]) + "…"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.state.Sessions[sessionID]
	if !ok {
		return
	}
	info.Title = title
	m.state.Sessions[sessionID] = info
	_ = m.save()
}

func (m *LocalManager) epochKey(channelName, channelUser string) string {
	return m.channelKey(channelName, channelUser)
}

func (m *LocalManager) GetEpoch(_ context.Context, channelName, channelUser string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Epochs[m.epochKey(channelName, channelUser)]
}

func (m *LocalManager) SetEpoch(_ context.Context, channelName, channelUser, day string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Epochs[m.epochKey(channelName, channelUser)] = day
	_ = m.save()
}

func (m *LocalManager) ListByChannel(_ context.Context, channelName, channelUser string) ([]Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sessions := make([]Info, 0)
	for _, info := range m.state.Sessions {
		if info.ChannelName == channelName && info.ChannelUser == channelUser {
			sessions = append(sessions, info)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	return sessions, nil
}

func (m *LocalManager) ListChannels(context.Context) ([]ChannelEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := make([]ChannelEntry, 0, len(m.state.Channels))
	for key, sid := range m.state.Channels {
		parts := strings.SplitN(key, ":", 2)
		chName, chUser := "", ""
		if len(parts) >= 1 {
			chName = dec(parts[0])
		}
		if len(parts) >= 2 {
			chUser = dec(parts[1])
		}
		entries = append(entries, ChannelEntry{
			ChannelName: chName,
			ChannelUser: chUser,
			SessionID:   sid,
			TTLSeconds:  -1,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ChannelName != entries[j].ChannelName {
			return entries[i].ChannelName < entries[j].ChannelName
		}
		return entries[i].ChannelUser < entries[j].ChannelUser
	})
	return entries, nil
}

func (m *LocalManager) GetChannelSession(_ context.Context, channelName, channelUser string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Channels[m.channelKey(channelName, channelUser)]
}

func (m *LocalManager) Get(_ context.Context, sessionID string) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.state.Sessions[sessionID]
	if !ok {
		return Info{}, fmt.Errorf("session not found: %s", sessionID)
	}
	return info, nil
}

func (m *LocalManager) UpdateAfterChat(_ context.Context, sessionID string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.state.Sessions[sessionID]
	if !ok {
		return
	}
	info.UpdatedAt = time.Now().Format(time.RFC3339)
	info.Count = count
	m.state.Sessions[sessionID] = info
	_ = m.save()
}

func (m *LocalManager) LoadMessages(context.Context, string) []*schema.Message {
	return nil
}
