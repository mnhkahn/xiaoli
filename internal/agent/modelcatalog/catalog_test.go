package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadCachesAndUsesETag(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == "etag-1" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", "etag-1")
		_, _ = w.Write([]byte(`{"version":1,"providers":{"deepseek":["deepseek-v4-flash"]}}`))
	}))
	defer server.Close()

	cfg := Config{Enabled: true, URL: server.URL, RefreshInterval: time.Nanosecond, Providers: map[string]Provider{}}
	path := filepath.Join(t.TempDir(), "model_catalog.json")
	first, err := Load(context.Background(), cfg, path)
	if err != nil || len(first.Providers["deepseek"]) != 1 {
		t.Fatalf("first Load() = %#v, %v", first, err)
	}
	time.Sleep(time.Millisecond)
	second, err := Load(context.Background(), cfg, path)
	if err != nil || len(second.Providers["deepseek"]) != 1 || requests != 2 {
		t.Fatalf("second Load() = %#v, %v; requests=%d", second, err, requests)
	}
}

func TestLoadFallsBackToCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model_catalog.json")
	if err := writeCache(path, cacheFile{FetchedAt: time.Now().Add(-time.Hour), Document: Document{Version: 1, Providers: map[string][]string{"deepseek": {"deepseek-v4-flash"}}}}); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(context.Background(), Config{Enabled: true, URL: "http://127.0.0.1:1", Timeout: time.Millisecond}, path)
	if err != nil || len(doc.Providers["deepseek"]) != 1 {
		t.Fatalf("Load() = %#v, %v", doc, err)
	}
}

func TestEntriesUsesAliasAndSkipsUnconfiguredProvider(t *testing.T) {
	entries := Entries(Document{Version: 1, Providers: map[string][]string{"deepseek": {"deepseek-v4-flash"}, "unknown": {"x"}}}, map[string]Provider{
		"deepseek": {BaseURL: "https://api.deepseek.com", APIKeyEnv: "DEEPSEEK_API_KEY", IDAliases: map[string]string{"deepseek-v4-flash": "deepseek:v4-flash"}},
	})
	if len(entries) != 1 || entries[0].ID != "deepseek:v4-flash" || entries[0].Model != "deepseek-v4-flash" {
		t.Fatalf("Entries() = %#v", entries)
	}
}
