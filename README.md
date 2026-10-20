<p align="center"><img src="assets/banner.svg" alt="agent-watch" width="720"></p>

A terminal app where a **master** agent leads **slave** coding agents, and you watch all of them live.

You chat with the master. When you agree on work, it shows you a plan. After your go, it gives each slave its own git worktree and a detailed brief, verifies their work itself (diffs, your tests, its own probes, cheat checks) and sends feedback until it's right. It never edits code itself; it has no edit tools.

Any of Claude Code and opencode can be the master or a slave, so Claude can lead opencode agents on cheap or free models, or the other way round.

## Quick start

**1. Have at least one agent CLI installed and logged in:** [Claude Code](https://claude.com/claude-code) (`claude`) and/or [opencode](https://opencode.ai) (`opencode`). You also need `git` and [Go](https://go.dev/dl/) 1.24+.

**2. Install:**

```bash
git clone https://github.com/n-delic/agent-watch && cd agent-watch
go build -o agent-watch .
mkdir -p ~/.local/bin
ln -s "$PWD/agent-watch" ~/.local/bin/agent-watch
ln -s "$PWD/agent-watch" ~/.local/bin/aw          # the short name
```

(`~/.local/bin` must be on your `PATH`.)

**3. Run it in the project you want to work on:**

```bash
cd ~/code/my-project
aw
```

**4. Pick your agents.** The first launch opens the setup: choose the master (agent, model, effort) and enable the slaves it may use, then `enter`. Change it any time with `ctrl+s`.

**5. Talk to the master** like you would to Claude Code:

> add rate limiting to the API client, use two slaves

It reads the code, proposes a plan (which slave does what, in which worktree) and waits. Say **go**, then watch the slaves work on the right. When they're done and verified, it asks before committing or merging anything.

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

The setup is saved to `~/.config/agent-watch/config.json`.

## Update

```bash
cd agent-watch && git pull && go build -o agent-watch .
```

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
