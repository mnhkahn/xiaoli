package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMiniEditorMovesAndInsertsText(t *testing.T) {
	ed := newMiniEditor("a.go", "one\ntwo\n")
	ed.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if ed.cursorY != 1 {
		t.Fatalf("cursorY = %d, want 1", ed.cursorY)
	}
	ed.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	ed.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	if got := ed.text(); got != "one\nXtwo\n" {
		t.Fatalf("text after insert = %q", got)
	}
	if ed.mode != editorInsert || !ed.dirty {
		t.Fatalf("mode=%s dirty=%v, want insert dirty", ed.mode, ed.dirty)
	}
	ed.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if ed.mode != editorNormal {
		t.Fatalf("mode = %s, want normal", ed.mode)
	}
}

func TestMiniEditorCommandModeParsesSaveAndQuit(t *testing.T) {
	ed := newMiniEditor("a.go", "one\n")
	ed.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	ed.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	result := ed.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if result.Command != "w" {
		t.Fatalf("command = %q, want w", result.Command)
	}
	ed.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	ed.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	result = ed.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if result.Command != "q" {
		t.Fatalf("command = %q, want q", result.Command)
	}
}

func TestMiniEditorSaveWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ed := newMiniEditor("a.txt", "new\n")
	ed.dirty = true
	if err := ed.save(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" || ed.dirty {
		t.Fatalf("saved data=%q dirty=%v", string(data), ed.dirty)
	}
}

func TestMiniEditorRenderShowsModeAndCursor(t *testing.T) {
	ed := newMiniEditor("a.go", "one\ntwo\n")
	got := stripTestANSI(ed.render(40, 8))
	if !strings.Contains(got, "NORMAL") || !strings.Contains(got, "one") {
		t.Fatalf("render = %q", got)
	}
	ed.mode = editorInsert
	got = stripTestANSI(ed.render(40, 8))
	if !strings.Contains(got, "INSERT") {
		t.Fatalf("render insert = %q", got)
	}
}
