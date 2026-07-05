package admin

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractDOCXTextReadsDocumentXML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.docx")
	writeTestDOCX(t, path, `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>第一段项目进展。</w:t></w:r></w:p>
    <w:p><w:r><w:t>第二段风险提醒。</w:t></w:r></w:p>
  </w:body>
</w:document>`)

	text, err := extractDOCXText(path)
	if err != nil {
		t.Fatalf("extractDOCXText() error = %v", err)
	}
	for _, want := range []string{"第一段项目进展", "第二段风险提醒"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text = %q, want it to contain %q", text, want)
		}
	}
}

func writeTestDOCX(t *testing.T, path, documentXML string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	part, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(documentXML)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}
