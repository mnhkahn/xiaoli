package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type shellDoneMsg struct {
	command string
	output  string
	err     error
	cwd     string
}

func isShellInput(value string) bool {
	return strings.HasPrefix(value, "!")
}

func shellCommand(value string) string {
	return strings.TrimSpace(strings.TrimPrefix(value, "!"))
}

func (msg shellDoneMsg) transcriptItem() transcriptItem {
	text := "$ " + strings.TrimSpace(msg.command)
	output := strings.TrimRight(msg.output, "\n")
	if output != "" {
		text += "\n" + output
	}
	if msg.err != nil {
		text += "\n" + msg.err.Error()
	}
	return transcriptItem{role: "shell", text: text}
}

func startShellCommand(ctx context.Context, cwd, command string) tea.Cmd {
	return func() tea.Msg {
		nextCWD := cwd
		output, err := runShellCommandContext(ctx, cwd, command)
		if err == nil {
			if changed, ok := parseShellCD(cwd, command); ok {
				nextCWD = changed
			}
		}
		return shellDoneMsg{command: command, output: output, err: err, cwd: nextCWD}
	}
}

func runShellCommand(cwd, command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return runShellCommandContext(ctx, cwd, command)
}

func runShellCommandContext(ctx context.Context, cwd, command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", nil
	}
	if changed, ok := parseShellCD(cwd, command); ok {
		return changed, nil
	}
	shell := os.Getenv("SHELL")
	if strings.TrimSpace(shell) == "" {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-lc", command)
	cmd.Dir = cwd
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return buf.String(), fmt.Errorf("command timed out after 2m")
	}
	if ctx.Err() == context.Canceled {
		return buf.String(), context.Canceled
	}
	return buf.String(), err
}

func parseShellCD(cwd, command string) (string, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 || fields[0] != "cd" || len(fields) > 2 {
		return "", false
	}
	target := ""
	if len(fields) == 1 {
		target = os.Getenv("HOME")
	} else {
		target = fields[1]
	}
	if strings.HasPrefix(target, "~") {
		target = filepath.Join(os.Getenv("HOME"), strings.TrimPrefix(target, "~"))
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(cwd, target)
	}
	clean := filepath.Clean(target)
	info, err := os.Stat(clean)
	if err != nil {
		return "", false
	}
	if !info.IsDir() {
		return "", false
	}
	return clean, true
}

func shellCompletions(value, cwd string, pathCommands []string) []slashSuggestion {
	if !isShellInput(value) {
		return nil
	}
	command := strings.TrimPrefix(value, "!")
	tokens := strings.Fields(command)
	trailingSpace := strings.HasSuffix(command, " ")
	if len(tokens) == 0 || (!trailingSpace && len(tokens) == 1) {
		return commandCompletions(strings.TrimSpace(command), pathCommands)
	}
	if len(tokens) >= 1 && tokens[0] == "git" && !trailingSpace && len(tokens) == 2 {
		return gitSubcommandCompletions(tokens[1])
	}
	prefix := ""
	if !trailingSpace && len(tokens) > 0 {
		prefix = tokens[len(tokens)-1]
	}
	base := command
	if !trailingSpace {
		if idx := strings.LastIndex(command, prefix); idx >= 0 {
			base = command[:idx]
		}
	}
	return pathCompletions(base, prefix, cwd)
}

func (m model) shellSuggestions(limit int) []slashSuggestion {
	items := shellCompletions(m.input.Value(), m.cwd, nil)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func (m *model) recordShellHistory(command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	m.shellHistory = append(m.shellHistory, command)
	m.shellHistIndex = 0
	m.shellHistDraft = ""
}

func (m *model) navigateShellHistory(direction int) bool {
	if len(m.shellHistory) == 0 || direction == 0 || m.busy || m.hasPendingOptions() {
		return false
	}
	if m.shellHistIndex == 0 {
		m.shellHistDraft = strings.TrimPrefix(m.input.Value(), "!")
	}
	next := m.shellHistIndex
	if direction < 0 {
		next++
	} else {
		next--
	}
	if next < 0 {
		return false
	}
	if next == 0 {
		m.shellHistIndex = 0
		m.input.SetValue("!" + m.shellHistDraft)
		m.input.CursorEnd()
		return true
	}
	if next > len(m.shellHistory) {
		return false
	}
	m.shellHistIndex = next
	m.input.SetValue("!" + m.shellHistory[len(m.shellHistory)-next])
	m.input.CursorEnd()
	return true
}

func commandCompletions(prefix string, commands []string) []slashSuggestion {
	if commands == nil {
		commands = pathExecutables()
	}
	seen := map[string]bool{}
	var out []slashSuggestion
	for _, name := range commands {
		if name == "" || seen[name] || !strings.HasPrefix(name, prefix) {
			continue
		}
		seen[name] = true
		out = append(out, slashSuggestion{Name: name, Description: "command", Kind: "shell"})
		if len(out) >= 20 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func gitSubcommandCompletions(prefix string) []slashSuggestion {
	names := []string{"add", "branch", "checkout", "commit", "diff", "fetch", "log", "pull", "push", "restore", "show", "status", "switch"}
	var out []slashSuggestion
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			out = append(out, slashSuggestion{Name: "git " + name, Description: "git subcommand", Kind: "shell"})
		}
	}
	return out
}

func pathCompletions(base, prefix, cwd string) []slashSuggestion {
	searchDir := cwd
	namePrefix := prefix
	if dir := filepath.Dir(prefix); dir != "." {
		searchDir = filepath.Join(cwd, dir)
		namePrefix = filepath.Base(prefix)
	}
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	var out []slashSuggestion
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, namePrefix) {
			continue
		}
		full := name
		if dir := filepath.Dir(prefix); dir != "." {
			full = filepath.Join(dir, name)
		}
		desc := "file"
		if entry.IsDir() {
			full += "/"
			desc = "directory"
		}
		out = append(out, slashSuggestion{Name: strings.TrimSpace(base + full), Description: desc, Kind: "shell"})
		if len(out) >= 20 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func pathExecutables() []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || seen[entry.Name()] {
				continue
			}
			info, err := entry.Info()
			if err != nil || info.Mode()&0o111 == 0 {
				continue
			}
			seen[entry.Name()] = true
			out = append(out, entry.Name())
		}
	}
	sort.Strings(out)
	return out
}

func applyShellCompletion(value, completion string) string {
	if !isShellInput(value) {
		return value
	}
	return "!" + strings.TrimSpace(completion)
}

func renderShellSuggestions(items []slashSuggestion, width int) string {
	var lines []string
	for i, item := range items {
		prefix := "  "
		if i == 0 {
			prefix = "> "
		}
		line := fmt.Sprintf("%s!%s", prefix, item.Name)
		if item.Description != "" {
			line += " - " + item.Description
		}
		lines = append(lines, hintStyle.Render(fitDisplay(line, width)))
	}
	if len(lines) > 0 {
		lines = append(lines, hintStyle.Render("Tab complete · Esc exit shell"))
	}
	return strings.Join(lines, "\n")
}
