# Xiaoli TUI

This directory contains the local Xiaoli terminal UI. It runs the Xiaoli agent
locally and stores local state under `~/.xiaoli`.

## Install

Install the local Xiaoli TUI into the current Go environment:

```bash
go install github.com/mnhkahn/xiaoli/tui/cmd/xiaoli@latest
```

For local development from this repository:

```bash
go install ./tui/cmd/xiaoli
```

Initialize local settings and secrets:

```bash
xiaoli -init
```

If no model is configured yet, init starts a short model wizard. Choose a
provider, enter the API key, and Xiaoli writes the matching model endpoint into
`~/.xiaoli/settings.json` while storing the key in `~/.xiaoli/secrets.json`.
Common providers include OpenRouter, SiliconFlow, Ark / 火山方舟, OpenAI
compatible, and custom OpenAI-compatible endpoints. You can edit both files
later if you need more models.

Run:

```bash
xiaoli
```

After exit, the TUI prints the current session ID and a continue command. Resume
that session later with:

```bash
xiaoli -s <session-id>
```

## Local Files

The local TUI reads shared skills from `~/.agents/skills` and Xiaoli-specific
skills from `~/.xiaoli/skills`. It loads prompts in this order when the files
exist:

1. `~/.agents/AGENT.md`
2. `~/.xiaoli/AGENT.md`
3. `~/.xiaoli/SOUL.md`

Runtime logs are written to `~/.xiaoli/logs/tui.log` so they do not corrupt the
terminal UI.

## Commands

The TUI reuses the shared slash command handler. Useful local commands include
`/cd <path>`, `/version`, `/upgrade`, `/skills`, `/model list`, `/model use <id>`,
`/usage`, `/sessions`, `/resume <id>`, `/session <id>`, `/memory list`, `/mcp`,
`/tasks`, `/log <keyword>`, and `/reminder list`. Local log search also supports
`/log --all`, `/log --tools`, and `/log --errors`. When bash is enabled and a
command needs approval, reply with `允许` or `拒绝` in the TUI.

For coding workflows, `/tree` opens a full-screen project browser and `/diff`
opens a full-screen Git changes browser. `/commit` generates a commit message
from the current staged diff; if nothing is staged, it stages the provided file
arguments, or falls back to `git add .`.

The TUI leaves mouse selection with the terminal by default, so drag-select
copying works normally. Press `Ctrl+O` (`⌃O`) to temporarily enable mouse mode
for wheel scrolling and mouse interactions, then press `Esc` or `Ctrl+O` to
return to copy-friendly selection. Press `Ctrl+T` (`⌃T`) to open `/tree`,
`Ctrl+K` (`⌃K`) to open `/diff`, and `Ctrl+S` (`⌃S`) to run Git sync.
Press `Tab` twice quickly to open the recent-project switcher. The switcher
stores recent TUI workspaces in `~/.xiaoli/state/tui_workspaces.json`; selecting
a project switches cwd, restores that project's session history when available,
and binds new chats back to that project.

File previews use syntax highlighting, diffs highlight metadata, hunks,
additions, and deletions. In `/tree`, press `Tab` or `l` on a file to focus the
right editor, use `h`/`j`/`k`/`l` to move, `i` to insert, `Esc` for normal mode,
and `:w`, `:q`, or `:wq` for save and close operations. In `/diff`, press
`Enter` or `Space` to stage or unstage the selected file.

Model-visible built-in tools in local TUI include web search, optional web
fetch, local memory, local run-log search, tasks, reminders stored at
`~/.xiaoli/state/reminders.json`, and `file_write` under `tools.allowed_roots`.
`bash` is available when `tools.bash` is true.

Use Up/Down, PgUp/PgDn, Ctrl+U/Ctrl+D, Home, and End to scroll inside the active
TUI pane. The two-line status bar sits below the input box: the first line keeps
status, model, cwd, Git state, context usage, and update hints visible; the
second line keeps keyboard shortcuts and common commands visible without taking
horizontal space from the transcript.

Release builds can inject a version with:

```bash
go build -ldflags "-X main.version=vX.Y.Z" ./tui/cmd/xiaoli
```

For `go install` distribution, publish versions as Git tags:

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

The TUI checks the latest GitHub release in the background at startup and caches
the result in `~/.xiaoli/state/version.json` for 24 hours. When a newer release
is available, the banner and status bar show a quiet update hint; release notes
from GitHub are shown in the welcome banner when present. `/upgrade` prints the
matching `go install github.com/mnhkahn/xiaoli/tui/cmd/xiaoli@vX.Y.Z` command
without running it automatically.
