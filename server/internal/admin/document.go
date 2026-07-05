package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxExtractedDocumentChars = 12000

type documentTextExtractor interface {
	ExtractText(ctx context.Context, path, fileName string) (string, error)
}

type documentTextExtractorFunc func(ctx context.Context, path, fileName string) (string, error)

func (f documentTextExtractorFunc) ExtractText(ctx context.Context, path, fileName string) (string, error) {
	return f(ctx, path, fileName)
}

type defaultDocumentTextExtractor struct{}

func (defaultDocumentTextExtractor) ExtractText(ctx context.Context, path, fileName string) (string, error) {
	switch supportedDocumentExt(fileName) {
	case ".pdf":
		return extractPDFText(ctx, path)
	case ".docx":
		return extractDOCXText(path)
	case ".doc":
		return extractDOCText(ctx, path)
	default:
		return "", fmt.Errorf("unsupported document type: %s", fileName)
	}
}

func supportedDocumentExt(fileName string) string {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	switch ext {
	case ".pdf", ".doc", ".docx":
		return ext
	default:
		return ""
	}
}

func truncateDocumentText(text string) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if len([]rune(text)) <= maxExtractedDocumentChars {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxExtractedDocumentChars]) + "\n[内容过长，已截断]"
}

func extractDOCXText(path string) (string, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer reader.Close()

	var parts []string
	for _, file := range reader.File {
		if file.Name != "word/document.xml" && !strings.HasPrefix(file.Name, "word/header") && !strings.HasPrefix(file.Name, "word/footer") {
			continue
		}
		text, err := extractDOCXXMLText(file)
		if err != nil {
			return "", err
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	text := truncateDocumentText(strings.Join(parts, "\n"))
	if text == "" {
		return "", errors.New("docx contains no readable text")
	}
	return text, nil
}

func extractDOCXXMLText(file *zip.File) (string, error) {
	rc, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("open docx xml: %w", err)
	}
	defer rc.Close()

	var b strings.Builder
	decoder := xml.NewDecoder(rc)
	for {
		tok, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("decode docx xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "p" && b.Len() > 0 {
				b.WriteByte('\n')
			}
		case xml.CharData:
			if s := strings.TrimSpace(string(t)); s != "" {
				if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
					b.WriteByte(' ')
				}
				b.WriteString(s)
			}
		}
	}
	return b.String(), nil
}

func extractPDFText(ctx context.Context, path string) (string, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return "", errors.New("pdftotext is not installed")
	}
	out, err := exec.CommandContext(ctx, "pdftotext", "-layout", "-q", path, "-").Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext failed: %w", err)
	}
	text := truncateDocumentText(string(out))
	if text == "" {
		return "", errors.New("pdf contains no readable text")
	}
	return text, nil
}

func extractDOCText(ctx context.Context, path string) (string, error) {
	if text, err := extractDOCTextWithTextutil(ctx, path); err == nil && strings.TrimSpace(text) != "" {
		return truncateDocumentText(text), nil
	}
	if text, err := extractDOCTextWithAntiword(ctx, path); err == nil && strings.TrimSpace(text) != "" {
		return truncateDocumentText(text), nil
	}
	if text, err := extractDOCTextWithSoffice(ctx, path); err == nil && strings.TrimSpace(text) != "" {
		return truncateDocumentText(text), nil
	}
	return "", errors.New("no available tool could read .doc; install textutil, antiword, or soffice")
}

func extractDOCTextWithTextutil(ctx context.Context, path string) (string, error) {
	if _, err := exec.LookPath("textutil"); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "textutil", "-convert", "txt", "-stdout", path).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func extractDOCTextWithAntiword(ctx context.Context, path string) (string, error) {
	if _, err := exec.LookPath("antiword"); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "antiword", path).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func extractDOCTextWithSoffice(ctx context.Context, path string) (string, error) {
	if _, err := exec.LookPath("soffice"); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "xiaoli-doc-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, "soffice", "--headless", "--convert-to", "txt:Text", "--outdir", dir, path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("soffice failed: %w %s", err, strings.TrimSpace(stderr.String()))
	}
	outPath := filepath.Join(dir, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".txt")
	raw, err := os.ReadFile(outPath)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
