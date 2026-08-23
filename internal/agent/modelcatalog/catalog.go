// Package modelcatalog loads an optional remote catalog of model names.
// Connection settings and secrets always remain in the local application config.
package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Enabled         bool                `json:"enabled"`
	URL             string              `json:"url"`
	RefreshInterval time.Duration       `json:"-"`
	Timeout         time.Duration       `json:"-"`
	Providers       map[string]Provider `json:"providers"`
}

type Provider struct {
	BaseURL       string            `json:"base_url"`
	APIKeyEnv     string            `json:"api_key_env"`
	MaxTokens     int               `json:"max_tokens"`
	ContextLength int               `json:"context_length"`
	IDAliases     map[string]string `json:"id_aliases"`
}

type Document struct {
	Version   int                 `json:"version"`
	UpdatedAt string              `json:"updated_at"`
	Providers map[string][]string `json:"providers"`
}

type cacheFile struct {
	FetchedAt time.Time `json:"fetched_at"`
	ETag      string    `json:"etag"`
	Document  Document  `json:"document"`
}

type Entry struct {
	ID            string
	Provider      string
	Model         string
	DisplayName   string
	BaseURL       string
	APIKeyEnv     string
	MaxTokens     int
	ContextLength int
}

func Load(ctx context.Context, cfg Config, cachePath string) (Document, error) {
	if !cfg.Enabled || strings.TrimSpace(cfg.URL) == "" {
		return Document{}, nil
	}
	cached, hasCache := readCache(cachePath)
	if hasCache && cfg.RefreshInterval > 0 && time.Since(cached.FetchedAt) < cfg.RefreshInterval {
		return cached.Document, nil
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return fallback(cached, hasCache, err)
	}
	if hasCache && cached.ETag != "" {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fallback(cached, hasCache, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && hasCache {
		cached.FetchedAt = time.Now().UTC()
		_ = writeCache(cachePath, cached)
		return cached.Document, nil
	}
	if resp.StatusCode != http.StatusOK {
		return fallback(cached, hasCache, fmt.Errorf("model catalog: unexpected status %s", resp.Status))
	}
	var doc Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fallback(cached, hasCache, fmt.Errorf("model catalog: decode response: %w", err))
	}
	if doc.Version != 1 || doc.Providers == nil {
		return fallback(cached, hasCache, fmt.Errorf("model catalog: unsupported document"))
	}
	_ = writeCache(cachePath, cacheFile{FetchedAt: time.Now().UTC(), ETag: resp.Header.Get("ETag"), Document: doc})
	return doc, nil
}

func Entries(doc Document, providers map[string]Provider) []Entry {
	entries := make([]Entry, 0)
	for providerID, names := range doc.Providers {
		provider, ok := providers[providerID]
		if !ok || strings.TrimSpace(provider.BaseURL) == "" || strings.TrimSpace(provider.APIKeyEnv) == "" {
			continue
		}
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			id := providerID + ":" + name
			if alias := strings.TrimSpace(provider.IDAliases[name]); alias != "" {
				id = alias
			}
			entries = append(entries, Entry{ID: id, Provider: providerID, Model: name, DisplayName: name, BaseURL: provider.BaseURL, APIKeyEnv: provider.APIKeyEnv, MaxTokens: provider.MaxTokens, ContextLength: provider.ContextLength})
		}
	}
	return entries
}

func fallback(cached cacheFile, hasCache bool, err error) (Document, error) {
	if hasCache {
		return cached.Document, nil
	}
	return Document{}, err
}

func readCache(path string) (cacheFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheFile{}, false
	}
	var cached cacheFile
	if json.Unmarshal(data, &cached) != nil || cached.Document.Version != 1 || cached.Document.Providers == nil {
		return cacheFile{}, false
	}
	return cached, true
}

func writeCache(path string, cached cacheFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
