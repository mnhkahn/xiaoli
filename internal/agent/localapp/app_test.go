package localapp

import (
	"path/filepath"
	"testing"
)

func TestNewFailsCleanlyWithoutModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := New(Options{ConfigPath: path}); err == nil {
		t.Fatal("New() error = nil, want missing model config error")
	}
}
