package channel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePathRejectsSiblingDirectoryWithSamePrefix(t *testing.T) {
	allowed := t.TempDir()
	outside := allowed + "-outside"
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "artifact.pdf")
	if err := os.WriteFile(outsideFile, []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ValidatePath(outsideFile, []string{allowed}); err == nil {
		t.Fatalf("ValidatePath(%q) = nil, want rejection outside allowed root %q", outsideFile, allowed)
	}
}
