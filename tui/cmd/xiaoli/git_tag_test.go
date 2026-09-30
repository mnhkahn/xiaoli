package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func tagTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	tagTestGit(t, dir, "init")
	tagTestGit(t, dir, "config", "user.email", "test@example.com")
	tagTestGit(t, dir, "config", "user.name", "Test")
	tagTestGit(t, dir, "commit", "--allow-empty", "-m", "initial")
	return dir
}
func TestGitTagVersions(t *testing.T) {
	v, err := highestGitTag("v1.3.8\nv1.3.10\nv1.2.99\nv9.0.0-rc1\nabc\nv01.0.0\n123 refs/tags/v1.4.0\n")
	if err != nil || v.String() != "v1.4.0" {
		t.Fatalf("%v %v", v, err)
	}
	for size, want := range map[string]string{"s": "v1.3.9", "m": "v1.4.0", "l": "v2.0.0"} {
		got, err := nextGitTag(gitTagVersion{numbers: [3]uint64{1, 3, 8}, prefix: "v"}, size)
		if err != nil || got != want {
			t.Fatalf("%s: %s %v", size, got, err)
		}
	}
	if _, err := nextGitTag(v, "major"); err == nil {
		t.Fatal("accepted unsupported size")
	}
	if _, err := nextGitTag(gitTagVersion{numbers: [3]uint64{0, 0, ^uint64(0)}, prefix: "v"}, "s"); err == nil {
		t.Fatal("overflow")
	}
}
func TestGitTagPrepareAndCreate(t *testing.T) {
	dir := tagTestRepo(t)
	tagTestGit(t, dir, "tag", "v1.3.8")
	ctx := context.Background()
	p, err := prepareGitTag(ctx, dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	if p.target != "v1.3.9" {
		t.Fatal(p.target)
	}
	if out := tagTestGit(t, dir, "tag", "--list", "v1.3.9"); strings.TrimSpace(out) != "" {
		t.Fatal("preview mutated tags")
	}
	result := executeGitTag(ctx, p, false, false)
	if result.err != nil || !result.created {
		t.Fatalf("%+v", result)
	}
	if got := strings.TrimSpace(tagTestGit(t, dir, "cat-file", "-t", "refs/tags/v1.3.9")); got != "tag" {
		t.Fatal(got)
	}
	if result := executeGitTag(ctx, p, false, false); result.err == nil {
		t.Fatal("overwrote existing tag")
	}
}
func TestGitTagRejectsDirtyOrChangedHEAD(t *testing.T) {
	dir := tagTestRepo(t)
	ctx := context.Background()
	p, err := prepareGitTag(ctx, dir, "l")
	if err != nil {
		t.Fatal(err)
	}
	tagTestGit(t, dir, "commit", "--allow-empty", "-m", "new head")
	if r := executeGitTag(ctx, p, false, false); r.err == nil || r.created {
		t.Fatal("accepted changed HEAD")
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareGitTag(ctx, dir, "s"); err == nil {
		t.Fatal("accepted dirty worktree")
	}
}
func TestGitTagRemoteAndRetry(t *testing.T) {
	dir := tagTestRepo(t)
	remote := t.TempDir()
	tagTestGit(t, remote, "init", "--bare")
	tagTestGit(t, dir, "remote", "add", "origin", remote)
	tagTestGit(t, dir, "tag", "v1.9.0")
	tagTestGit(t, dir, "push", "origin", "refs/tags/v1.9.0")
	tagTestGit(t, dir, "tag", "-d", "v1.9.0")
	ctx := context.Background()
	p, err := prepareGitTag(ctx, dir, "m")
	if err != nil || p.target != "v1.10.0" {
		t.Fatalf("%+v %v", p, err)
	}
	// Make push fail after preview; retry must reuse the created tag.
	hook := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := executeGitTag(ctx, p, true, false)
	if r.err == nil || !r.created {
		t.Fatalf("%+v", r)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	r = executeGitTag(ctx, p, true, true)
	if r.err != nil {
		t.Fatal(r.err)
	}
	tags := tagTestGit(t, remote, "tag", "--list")
	if !strings.Contains(tags, "v1.10.0") || strings.Contains(tags, "v1.11.0") {
		t.Fatal(tags)
	}
}
func TestGitTagInputAndSuggestions(t *testing.T) {
	dir := tagTestRepo(t)
	m := model{cwd: dir, input: textinput.New(), viewport: viewport.New(80, 24), width: 80, height: 30}
	m.input.SetValue("/tag ")
	if got := m.slashSuggestions(8); len(got) != 3 || got[0].Name != "tag s" {
		t.Fatalf("%+v", got)
	}
	m.input.SetValue("/tag")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd == nil || !m.busy {
		t.Fatal("tag did not start")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if m.pendingGitTag.stage != "select" || !strings.Contains(m.pendingQuestion, "/tag s") || len(m.pendingOptions) != 4 {
		t.Fatalf("%+v", m.pendingGitTag)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.pendingGitTag.stage != "confirm" || m.pendingGitTag.plan.target != "v0.0.1" {
		t.Fatalf("%+v", m.pendingGitTag)
	}
	if tags := strings.TrimSpace(tagTestGit(t, dir, "tag", "--list")); tags != "" {
		t.Fatal("selection created tag")
	}
	m.input.SetValue("取消操作")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.pendingGitTag.stage != "" || m.hasPendingOptions() {
		t.Fatal("cancel failed")
	}
}

func tagTestGit(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	out, err := runGitCombined(cwd, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

func TestGitTagDirectCommandsAndInvalidInput(t *testing.T) {
	for _, size := range []string{"s", "m", "l", "major"} {
		t.Run(size, func(t *testing.T) {
			dir := tagTestRepo(t)
			m := model{cwd: dir, input: textinput.New(), viewport: viewport.New(80, 24), width: 80, height: 30}
			m.input.SetValue("/tag " + size)
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(model)
			if size == "major" {
				if cmd != nil || m.busy || len(m.items) == 0 || !strings.Contains(m.items[len(m.items)-1].text, "/tag s") {
					t.Fatal("invalid command was not handled locally")
				}
				return
			}
			if cmd == nil {
				t.Fatal("missing prepare")
			}
			next, _ = m.Update(cmd())
			m = next.(model)
			if m.pendingGitTag.stage != "confirm" {
				t.Fatalf("%+v", m.pendingGitTag)
			}
			next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(model)
			if cmd == nil {
				t.Fatal("missing create")
			}
			next, _ = m.Update(cmd())
			m = next.(model)
			if m.busy || m.hasPendingOptions() || m.pendingGitTag.stage != "" {
				t.Fatal("did not finish")
			}
			want := map[string]string{"s": "v0.0.1", "m": "v0.1.0", "l": "v1.0.0"}[size]
			if tags := strings.TrimSpace(tagTestGit(t, dir, "tag", "--list")); tags != want {
				t.Fatal(tags)
			}
		})
	}
}

func TestGitTagPrefixCompatibility(t *testing.T) {
	for _, tt := range []struct{ name, refs, previous, small, medium, large string }{
		{"homework", "v0.0.1\n0.1.8\n0.1.9", "0.1.9", "0.1.10", "0.2.0", "1.0.0"},
		{"lowercase", "0.1.9\nv1.3.8", "v1.3.8", "v1.3.9", "v1.4.0", "v2.0.0"},
		{"uppercase", "v0.0.1\n123 refs/tags/V1.3.8", "V1.3.8", "V1.3.9", "V1.4.0", "V2.0.0"},
		{"numeric order", "0.1.9\n0.1.10\nv0.1.8", "0.1.10", "0.1.11", "0.2.0", "1.0.0"},
		{"zero", "0.0.0", "0.0.0", "0.0.1", "0.1.0", "1.0.0"},
		{"empty", "", "v0.0.0", "v0.0.1", "v0.1.0", "v1.0.0"},
		{"ignore invalid", "9.0.0-rc1\nV9.0.0+build\n01.0.0\n0.1.9", "0.1.9", "0.1.10", "0.2.0", "1.0.0"},
		{"equal", "0.1.9\nV0.1.9\nv0.1.9", "v0.1.9", "v0.1.10", "v0.2.0", "v1.0.0"},
		{"equal reversed", "v0.1.9\nV0.1.9\n0.1.9", "v0.1.9", "v0.1.10", "v0.2.0", "v1.0.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v, err := highestGitTag(tt.refs)
			if err != nil || v.String() != tt.previous {
				t.Fatalf("version = %s, %v; want %s", v, err, tt.previous)
			}
			for size, want := range map[string]string{"s": tt.small, "m": tt.medium, "l": tt.large} {
				got, err := nextGitTag(v, size)
				if err != nil || got != want {
					t.Fatalf("%s = %s, %v; want %s", size, got, err, want)
				}
			}
		})
	}
}

func TestGitTagUnprefixedRemote(t *testing.T) {
	dir := tagTestRepo(t)
	remote := t.TempDir()
	tagTestGit(t, remote, "init", "--bare")
	tagTestGit(t, dir, "remote", "add", "origin", remote)
	tagTestGit(t, dir, "tag", "v0.0.1")
	tagTestGit(t, dir, "tag", "0.1.9")
	tagTestGit(t, dir, "push", "origin", "refs/tags/0.1.9")
	tagTestGit(t, dir, "tag", "-d", "0.1.9")
	p, err := prepareGitTag(context.Background(), dir, "s")
	if err != nil || p.previous.String() != "0.1.9" || p.target != "0.1.10" {
		t.Fatalf("%+v %v", p, err)
	}
	r := executeGitTag(context.Background(), p, true, false)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := strings.TrimSpace(tagTestGit(t, remote, "tag", "--list", "0.1.10")); got != "0.1.10" {
		t.Fatal(got)
	}
}
