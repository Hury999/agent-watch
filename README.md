# agent-watch

A terminal app where a **master** agent leads **slave** coding agents, and you watch all of them live.

You chat with the master on the left. When you agree on work, the master shows you a plan. After your go, it creates a git worktree per slave, briefs each slave, verifies their work itself (diff, tests, cheat checks), and sends feedback until it's right. It never edits code itself; it has no edit tools.

```
┌─ MASTER · claude sonnet high ─────┬─ SLAVES ──────────────────────────┐
│ › add a parser and CLI flags      │ ● parser  opencode-personal  r1   │
│ ⚙ Bash git worktree add …         │ ✓ cli     opencode-decomp    r2   │
│ Plan: parser → opencode-personal… ├─ PARSER · live ───────────────────┤
│ › go                              │ ⚙ edit parser.py                  │
│ » slave cli: turn 2 finished      │ ⚙ shell python3 test_parser.py    │
│ Verified cli in 2 rounds…         │   OK                              │
├───────────────────────────────────┴───────────────────────────────────┤
│ > _                                                                   │
└───────────────────────────────────────────────────────────────────────┘
```

What you see:

- Transcripts in Claude Code style (`⏺` messages and tool calls, `⎿` output), with agents' markdown rendered: headings, lists, tables, code.
- A live spinner per agent with elapsed time, tokens (↑ in ↓ out) and cost; every turn ends with how long it took.
- A header with Claude's 5-hour and 7-day usage (from its own rate limit reports) and each opencode account's spend today and over 7 days (`opencode stats`; opencode has no limits API).

## Agents

| Kind | Runs |
|---|---|
| `claude` | `claude -p --output-format stream-json` |
| `opencode-decomp` | `opencode run --format json` (default account, shared background service) |
| `opencode-personal` | the same with `OPENCODE_DB=~/.local/share/opencode/personal.db --standalone` |

Any of them can be the master; slaves are the kinds you enable in setup.

## Build and run

```bash
go build -o agent-watch .
cd ~/code/some-repo && ~/projects/agent-watch/agent-watch
```

The first launch opens the setup (master agent/model/effort, which slaves are enabled and with what model). It's saved to `~/.config/agent-watch/config.json`; reopen it with `ctrl+s`.

## Keys

| Key | |
|---|---|
| `enter` | send to the master (or to the slave you're messaging) |
| `tab` | switch between input and slave list |
| `↑↓` | select a slave (in the list) |
| `enter` (list) | selected slave fullscreen |
| `m` (list) | message the selected slave directly; the master is told |
| `x` (list) | stop the selected slave |
| `pgup` / `pgdn` | scroll the master (or the slave when the list is focused) |
| `esc` | back |
| `ctrl+s` | setup |
| `ctrl+c` | quit (asks again if agents are running, then stops them) |
| click | a slave selects it, again opens it fullscreen; the fullscreen title closes it; a pane or the input focuses it |
| wheel | scrolls the pane under the pointer |

Mouse capture means selecting text needs `shift` + drag in most terminals.

## How it works

- The master gets `master.md` as its instructions and drives slaves with `agent-watch spawn|send|stop`. Those commands hand a request file to the running TUI, which validates it against your setup and starts the slave.
- When a slave finishes a turn, the TUI wakes the master with the slave's final reply.
- Each turn has a 45-minute timeout. A stopped or timed-out turn is also interrupted on opencode's shared service, which would otherwise keep running it.
- Raw output of every agent is logged in `~/.local/state/agent-watch/<run>/<name>.jsonl`.

## Limits

- Every launch is a fresh run; quitting ends the master's and slaves' conversations.
- Slaves run with full permissions (`--auto` / `bypassPermissions`), which is why the master gives each its own worktree.
- Claude's token counts arrive at the end of each of its turns; opencode's update after every step.
- Not yet supported: Codex, local model servers.
