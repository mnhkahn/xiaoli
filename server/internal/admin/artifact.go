package admin

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Artifact represents a temporary file accessible via HTTP
type Artifact struct {
	ID          string
	Token       string
	Path        string
	DisplayName string
	MIMEType    string
	Size        int64
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// ArtifactStore manages temporary artifacts with TTL-based cleanup
type ArtifactStore struct {
	mu         sync.Mutex
	artifacts  map[string]Artifact
	maxSize    int64
	defaultTTL time.Duration
}

type ArtifactConfig struct {
	MaxSize    int64         // Max file size in bytes
	DefaultTTL time.Duration // Default TTL for artifacts
}

func NewArtifactStore(cfg ArtifactConfig) *ArtifactStore {
	if cfg.MaxSize == 0 {
		cfg.MaxSize = 20 * 1024 * 1024 // 20MB default
	}
	if cfg.DefaultTTL == 0 {
		cfg.DefaultTTL = time.Hour
	}
	s := &ArtifactStore{
		artifacts:  make(map[string]Artifact),
		maxSize:    cfg.MaxSize,
		defaultTTL: cfg.DefaultTTL,
	}
	go s.cleanupLoop()
	return s
}

// Store stores a file as an artifact. Path must be absolute.
// ttl <= 0 means use default TTL.
func (s *ArtifactStore) Store(path, displayName, mimeType string, ttl time.Duration) (Artifact, error) {
	// Must be absolute path
	if !filepath.IsAbs(path) {
		return Artifact{}, fmt.Errorf("path must be absolute")
	}
	cleanPath := filepath.Clean(path)

	// Check file exists and is not a symlink
	info, err := os.Lstat(cleanPath)
	if err != nil {
		return Artifact{}, fmt.Errorf("stat failed: %w", err)
	}

	// Disallow symlinks for security
	if info.Mode()&os.ModeSymlink != 0 {
		return Artifact{}, fmt.Errorf("symlinks not allowed")
	}

	// Must be a regular file
	if !info.Mode().IsRegular() {
		return Artifact{}, fmt.Errorf("not a regular file")
	}

	// Size check
	if info.Size() > s.maxSize {
		return Artifact{}, fmt.Errorf("file too large: %d bytes (max %d)", info.Size(), s.maxSize)
	}

	// MIME type detection
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(cleanPath))
		if mimeType == "" {
			// Try reading file header
			f, err := os.Open(cleanPath)
			if err == nil {
				buf := make([]byte, 512)
				n, _ := f.Read(buf)
				f.Close()
				if n > 0 {
					mimeType = http.DetectContentType(buf[:n])
				}
			}
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	// Display name
	if displayName == "" {
		displayName = filepath.Base(cleanPath)
	} else {
		displayName = filepath.Base(displayName)
	}

	// TTL handling
	if ttl <= 0 {
		ttl = s.defaultTTL
	}

	id := randomToken(16)
	token := randomToken(24)
	now := time.Now()

	artifact := Artifact{
		ID:          id,
		Token:       token,
		Path:        cleanPath,
		DisplayName: displayName,
		MIMEType:    mimeType,
		Size:        info.Size(),
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}

	s.mu.Lock()
	s.artifacts[id] = artifact
	s.mu.Unlock()

	return artifact, nil
}

// Get retrieves an artifact by ID and token.
// Returns (Artifact{}, false) if not found, token invalid, or expired.
func (s *ArtifactStore) Get(id, token string) (Artifact, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.artifacts[id]
	if !ok {
		return Artifact{}, false
	}
	if artifact.Token != token {
		return Artifact{}, false
	}
	if time.Now().After(artifact.ExpiresAt) {
		return Artifact{}, false
	}
	return artifact, true
}

// URL builds the public access URL for an artifact.
// baseURL should include scheme and host, e.g., "https://example.com".
func (s *ArtifactStore) URL(baseURL string, art any) string {
	a, ok := art.(Artifact)
	if !ok {
		return ""
	}
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	return baseURL + "/artifacts/" + a.ID + "?token=" + a.Token
}

func (s *ArtifactStore) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.cleanupOnce()
	}
}

func (s *ArtifactStore) cleanupOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, art := range s.artifacts {
		if now.After(art.ExpiresAt) {
			delete(s.artifacts, id)
		}
	}
}

func (s *AdminServer) handleArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Path[len("/artifacts/"):]
	if id == "" {
		http.Error(w, "missing artifact id", http.StatusNotFound)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusNotFound)
		return
	}

	art, ok := s.artifactStore.Get(id, token)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Verify file still exists
	_, err := os.Stat(art.Path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", art.MIMEType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", art.Size))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": art.DisplayName}))
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFile(w, r, art.Path)
}
