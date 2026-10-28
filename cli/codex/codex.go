// Package codex runs OpenAI's Codex (`codex exec`) as an agent.
package codex

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/n-delic/agent-watch/cli"
)

type CLI struct{ cli.Base }

func (CLI) Name() string { return "codex" }

var efforts = []string{"", "minimal", "low", "medium", "high", "xhigh"}

func (CLI) Efforts() []string { return efforts }

// Accounts are other homes, by convention ~/.codex-<name>, used as CODEX_HOME.
func (CLI) Accounts() ([]string, bool) { return cli.Dirs("~/.codex-*"), true }

// Usage is "limits": Limits reads them from its session file after each turn.
func (CLI) Usage() string { return "limits" }

// Command starts a session codex names itself (thread.started), and resumes it by that id after.
func (CLI) Command(t cli.Turn) (string, []string, []string) {
	var env []string
	if t.Account != "" {
		env = []string{"CODEX_HOME=" + t.Account}
	}
	// shortcut: codex can't keep a lead from editing and still let it run git and aw, so its no-edit rule is
	// only in its instructions; give the lead the sandbox's read-only mode if codex ever allows writable roots there.
	args := []string{"exec"}
	if t.Session != "" {
		args = append(args, "resume")
	}
	args = append(args, "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox")
	for _, img := range t.Images {
		args = append(args, "-i", img)
	}
	if t.Model != "" {
		args = append(args, "-m", t.Model)
	}
	if t.Effort != "" && slices.Contains(efforts, t.Effort) { // a known word only: it goes into TOML
		args = append(args, "-c", `model_reasoning_effort="`+t.Effort+`"`)
	}
	prompt := t.Prompt
	if t.Session != "" {
		args = append(args, t.Session)
	} else {
		prompt = cli.WithSystem(t.System, prompt) // codex has no system-prompt flag
	}
	return "codex", append(args, prompt), env
}

func (CLI) NewParser() cli.Parser { return parse }

// parse reads `codex exec --json`: a thread id, then items as they start and complete, and usage per turn.
func parse(raw []byte) ([]cli.Line, cli.Delta) {
	var e struct {
		Type     string
		ThreadID string `json:"thread_id"`
		Message  string
		Error    struct{ Message string }
		Usage    struct {
			Input     int64 `json:"input_tokens"` // the cached ones included
			Output    int64 `json:"output_tokens"`
			Reasoning int64 `json:"reasoning_output_tokens"`
		}
		Item struct {
			Type, Text, Command, Tool, Message string
			Output                             string `json:"aggregated_output"`
			ExitCode                           *int   `json:"exit_code"`
			Changes                            []struct{ Path, Kind string }
		}
	}
	if json.Unmarshal(raw, &e) != nil {
		return []cli.Line{{Kind: "info", Text: string(raw)}}, cli.Delta{}
	}
	it := e.Item
	switch e.Type {
	case "thread.started":
		return nil, cli.Delta{Session: e.ThreadID}
	case "turn.completed":
		return nil, cli.Delta{In: e.Usage.Input, Out: e.Usage.Output + e.Usage.Reasoning}
	case "turn.failed":
		return []cli.Line{{Kind: "err", Text: e.Error.Message}}, cli.Delta{}
	case "error":
		return []cli.Line{{Kind: "err", Text: e.Message}}, cli.Delta{}
	case "item.started":
		switch it.Type {
		case "command_execution":
			return []cli.Line{cli.ToolLine("shell", map[string]any{"command": unwrapShell(it.Command)})}, cli.Delta{}
		case "mcp_tool_call":
			return []cli.Line{{Kind: "tool", Text: it.Tool}}, cli.Delta{}
		}
	case "item.completed":
		switch it.Type {
		case "agent_message":
			return []cli.Line{{Kind: "text", Text: strings.TrimSpace(it.Text)}}, cli.Delta{}
		case "reasoning":
			if t := strings.TrimSpace(it.Text); t != "" {
				return []cli.Line{{Kind: "think", Text: t}}, cli.Delta{}
			}
		case "command_execution":
			lines := cli.OutLines(it.Output)
			if it.ExitCode != nil && *it.ExitCode != 0 {
				lines = append(lines, cli.Line{Kind: "err", Text: fmt.Sprintf("exit code %d", *it.ExitCode)})
			}
			return lines, cli.Delta{}
		case "file_change":
			var lines []cli.Line
			for _, c := range it.Changes {
				lines = append(lines, cli.Line{Kind: "tool", Text: cmp.Or(c.Kind, "edit"), Arg: c.Path})
			}
			return lines, cli.Delta{}
		case "error":
			return []cli.Line{{Kind: "err", Text: it.Message}}, cli.Delta{}
		}
	}
	return nil, cli.Delta{}
}

// unwrapShell shows `/bin/bash -lc 'cat x'` as `cat x`.
func unwrapShell(c string) string {
	if rest, ok := strings.CutPrefix(c, "/bin/bash -lc "); ok && len(rest) >= 2 && (rest[0] == '\'' || rest[0] == '"') && rest[len(rest)-1] == rest[0] {
		return rest[1 : len(rest)-1]
	}
	return c
}

// Limits reads an account's rate limit windows from a session's file, which has them after every turn
// (its --json output doesn't).
func (CLI) Limits(acct, session string) *cli.Limits {
	home := cmp.Or(acct, cli.Expand("~/.codex"))
	files, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+session+".jsonl"))
	if session == "" || len(files) == 0 {
		return nil
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		return nil
	}
	type window struct {
		UsedPercent   float64 `json:"used_percent"`
		WindowMinutes int     `json:"window_minutes"`
		ResetsAt      int64   `json:"resets_at"`
	}
	var last *cli.Limits
	for _, line := range strings.Split(string(b), "\n") {
		var e struct {
			Payload struct {
				Type       string
				RateLimits *struct{ Primary, Secondary *window } `json:"rate_limits"`
			}
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Payload.Type != "token_count" || e.Payload.RateLimits == nil {
			continue
		}
		l := &cli.Limits{}
		for _, w := range []*window{e.Payload.RateLimits.Primary, e.Payload.RateLimits.Secondary} {
			if w != nil {
				l.Windows = append(l.Windows, cli.Window{Label: windowLabel(w.WindowMinutes), Used: w.UsedPercent / 100, Reset: w.ResetsAt})
			}
		}
		last = l
	}
	return last
}

// windowLabel names a window by its length: 300 minutes is "5h", 10080 "7d".
func windowLabel(minutes int) string {
	switch {
	case minutes > 0 && minutes%1440 == 0:
		return fmt.Sprintf("%dd", minutes/1440)
	case minutes > 0 && minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	}
	return fmt.Sprintf("%dm", minutes)
}
