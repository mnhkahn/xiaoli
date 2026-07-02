# Xiaoli TUI

This directory contains the local Xiaoli terminal UI. It runs the Xiaoli agent
locally and stores local state under `~/.xiaoli`.

## Install

Install the local Xiaoli TUI into the current Go environment:

```bash
go install github.com/mnhkahn/xiaoli-esp32/tui/cmd/xiaoli-tui@latest
```

For local development from this repository:

```bash
go install ./tui/cmd/xiaoli-tui
```

Initialize local settings and secrets:

```bash
xiaoli-tui -init
```

Then edit `~/.xiaoli/settings.json` to set `models.default` and model
endpoints, and put API keys in `~/.xiaoli/secrets.json` or environment
variables. Run:

```bash
xiaoli-tui
```

After exit, the TUI prints the current session ID and a continue command. Resume
that session later with:

```bash
xiaoli-tui -s <session-id>
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
`/skills`, `/model list`, `/model use <id>`, `/sessions`, `/resume <id>`,
`/session <id>`, `/memory list`, `/mcp`, `/tasks`, `/log <keyword>`, and
`/reminder list`. Local log search also supports `/log --all`, `/log --tools`,
and `/log --errors`. When bash is enabled and a command needs approval, reply
with `允许` or `拒绝` in the TUI.

For coding workflows, `/tree` opens a full-screen project browser and `/diff`
opens a full-screen Git changes browser. `/commit` generates a commit message
from the current staged diff; if nothing is staged, it stages the provided file
arguments, or falls back to `git add .`.

The TUI keeps mouse capture off by default so terminal text selection works
everywhere. Press `Ctrl+O` (`⌃O`) to temporarily toggle mouse selection and
wheel scrolling. Press `Ctrl+K` (`⌃K`) to open `/diff`. Press `Ctrl+S` (`⌃S`) to
run the right-sidebar Git sync action.

File previews use syntax highlighting, diffs highlight metadata, hunks,
additions, and deletions. In `/tree`, press `Tab` or `l` on a file to focus the
right editor, use `h`/`j`/`k`/`l` to move, `i` to insert, `Esc` for normal mode,
and `:w`, `:q`, or `:wq` for save and close operations. In `/diff`, press
`Enter` or `Space` to stage or unstage the selected file.

Model-visible built-in tools in local TUI include web search, optional web
fetch, local memory, local run-log search, tasks, reminders stored at
`~/.xiaoli/state/reminders.json`, and `file_write` under `tools.allowed_roots`.
`bash` is available when `tools.bash` is true.

Use the mouse wheel, Up/Down, PgUp/PgDn, Ctrl+U/Ctrl+D, Home, and End to scroll
inside the TUI when mouse mode is enabled. The right sidebar keeps status,
model, cwd, context usage, task/MCP summaries, and key hints visible with a
fixed-priority layout.
