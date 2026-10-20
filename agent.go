package main

// One turn of an agent CLI: the command that runs it, and its JSON output turned into display lines.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Line is one displayed block of a transcript.
type Line struct {
	Kind string // you, text, think, tool (Text is the tool, Arg its argument), out, err, info, done, fail
	Text string
	Arg  string
	r    string // rendered block, cached for width rw
	rw   int
}

// Delta is what one output line adds to an agent's totals.
type Delta struct {
	Cost    float64
	In, Out int64
	Limits  *Limits // claude only: the account's rate limit windows
}

// Limits is how much of Claude's 5-hour and 7-day windows is used (0..1) and when each resets (unix seconds).
type Limits struct {
	Five, Seven           float64
	FiveReset, SevenReset int64
}

// newSession makes a session id: claude wants a UUID, opencode wants "ses" + anything.
func newSession(kind string) string {
	b := make([]byte, 16)
	rand.Read(b)
	if kind == "claude" {
		b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	}
	return "ses_aw" + hex.EncodeToString(b[:8])
}

// command builds one turn. A non-empty system prompt marks the master.
func command(a Agent, session string, first bool, prompt, system, dir string) *exec.Cmd {
	if strings.HasPrefix(prompt, "-") {
		prompt = " " + prompt // or the CLI reads it as a flag
	}
	var cmd *exec.Cmd
	if a.Agent == "claude" {
		args := []string{"-p", "--output-format", "stream-json", "--verbose", "--model", a.Model}
		if a.Effort != "" {
			args = append(args, "--effort", a.Effort)
		}
		if first {
			args = append(args, "--session-id", session)
		} else {
			args = append(args, "--resume", session)
		}
		if system != "" {
			// No edit tools: the master must not fix slave code itself.
			args = append(args, "--allowedTools", "Bash Read Grep Glob", "--append-system-prompt", system)
		} else {
			args = append(args, "--permission-mode", "bypassPermissions")
		}
		cmd = exec.Command("claude", append(args, prompt)...)
		cmd.Env = os.Environ()
	} else {
		model := a.Model
		if a.Effort != "" {
			model += "#" + a.Effort
		}
		args := []string{"run", "--session", session, "--model", model, "--auto", "--format", "json"}
		if system != "" {
			args = append(args, "--agent", "plan") // opencode's agent that can't edit files
			if first {
				prompt = system + "\n\n---\n\n" + prompt
			}
		}
		cmd = exec.Command("opencode", args...)
		cmd.Env = os.Environ()
		if a.Agent == "opencode-personal" {
			home, _ := os.UserHomeDir()
			cmd.Env = append(cmd.Env, "OPENCODE_DB="+filepath.Join(home, ".local/share/opencode/personal.db"))
			cmd.Args = append(cmd.Args, "--standalone")
		}
		cmd.Args = append(cmd.Args, prompt)
	}
	// stdin stays nil (/dev/null): opencode waits forever on an open non-tty stdin.
	cmd.Dir = dir
	cmd.Env = append(cmd.Env, "PWD="+dir)                 // opencode takes its directory from $PWD, which an explicit Env leaves stale
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // so stop can kill the whole tree
	return cmd
}

// interrupt stops a session server-side. Only needed for opencode's shared background service
// (opencode-decomp), which keeps running a session after its client is killed.
func interrupt(a Agent, session string) {
	if a.Agent == "opencode-decomp" {
		exec.Command("opencode", "api", "session.interrupt", "--param", "sessionID="+session).Run()
	}
}

// parse turns one line of an agent's JSON output into display lines and what it adds to the totals.
func parse(kind string, raw []byte) ([]Line, Delta) {
	if strings.TrimSpace(string(raw)) == "" {
		return nil, Delta{}
	}
	if kind == "claude" {
		return parseClaude(raw)
	}
	return parseOpencode(raw)
}

func parseOpencode(raw []byte) ([]Line, Delta) {
	var e struct {
		Type string
		Part struct {
			Text   string
			Tool   string
			Cost   float64
			Tokens struct {
				Input, Output, Reasoning int64
				Cache                    struct{ Read, Write int64 }
			}
			State struct {
				Input  map[string]any
				Output string
				Error  string
			}
		}
		Error any
	}
	if json.Unmarshal(raw, &e) != nil {
		return []Line{{Kind: "info", Text: string(raw)}}, Delta{}
	}
	p := e.Part
	switch e.Type {
	case "text":
		return []Line{{Kind: "text", Text: strings.TrimSpace(p.Text)}}, Delta{}
	case "reasoning":
		return []Line{{Kind: "think", Text: strings.TrimSpace(p.Text)}}, Delta{}
	case "tool_use":
		lines := []Line{toolLine(p.Tool, p.State.Input)}
		if p.State.Error != "" {
			return append(lines, Line{Kind: "err", Text: clip(p.State.Error, 300)}), Delta{}
		}
		return append(lines, outLines(p.State.Output)...), Delta{}
	case "step_finish":
		t := p.Tokens
		return nil, Delta{Cost: p.Cost, In: t.Input + t.Cache.Read + t.Cache.Write, Out: t.Output + t.Reasoning}
	case "error":
		b, _ := json.Marshal(e.Error)
		return []Line{{Kind: "err", Text: string(b)}}, Delta{}
	}
	return nil, Delta{}
}

func parseClaude(raw []byte) ([]Line, Delta) {
	var e struct {
		Type    string
		IsError bool `json:"is_error"`
		Result  string
		Cost    float64 `json:"total_cost_usd"`
		Usage   struct {
			Input         int64 `json:"input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			Output        int64 `json:"output_tokens"`
		}
		RateLimitInfo struct {
			UnifiedWindows struct {
				FiveHour struct {
					Utilization float64
					ResetsAt    int64
				} `json:"five_hour"`
				SevenDay struct {
					Utilization float64
					ResetsAt    int64
				} `json:"seven_day"`
			}
		} `json:"rate_limit_info"`
		Message struct {
			Content json.RawMessage
		}
	}
	if json.Unmarshal(raw, &e) != nil {
		return []Line{{Kind: "info", Text: string(raw)}}, Delta{}
	}
	switch e.Type {
	case "result":
		u := e.Usage
		d := Delta{Cost: e.Cost, In: u.Input + u.CacheCreation + u.CacheRead, Out: u.Output}
		if e.IsError {
			return []Line{{Kind: "err", Text: e.Result}}, d
		}
		return nil, d
	case "rate_limit_event":
		w := e.RateLimitInfo.UnifiedWindows
		return nil, Delta{Limits: &Limits{w.FiveHour.Utilization, w.SevenDay.Utilization, w.FiveHour.ResetsAt, w.SevenDay.ResetsAt}}
	case "assistant", "user":
	default:
		return nil, Delta{}
	}
	var blocks []struct {
		Type     string
		Text     string
		Thinking string
		Name     string
		Input    map[string]any
		Content  any
		IsError  bool `json:"is_error"`
	}
	json.Unmarshal(e.Message.Content, &blocks) // a plain-string content has nothing to show
	var lines []Line
	for _, b := range blocks {
		switch b.Type {
		case "text":
			lines = append(lines, Line{Kind: "text", Text: strings.TrimSpace(b.Text)})
		case "thinking":
			if t := strings.TrimSpace(b.Thinking); t != "" {
				lines = append(lines, Line{Kind: "think", Text: t})
			}
		case "tool_use":
			lines = append(lines, toolLine(b.Name, b.Input))
		case "tool_result":
			out := resultText(b.Content)
			if b.IsError {
				lines = append(lines, Line{Kind: "err", Text: clip(out, 300)})
			} else {
				lines = append(lines, outLines(out)...)
			}
		}
	}
	return lines, Delta{}
}

// resultText reads a tool result's content: a string, or a list of text blocks.
func resultText(c any) string {
	if s, ok := c.(string); ok {
		return s
	}
	var parts []string
	if list, ok := c.([]any); ok {
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func toolLine(name string, in map[string]any) Line {
	for _, k := range []string{"command", "file_path", "filePath", "path", "pattern", "url", "query", "description"} {
		if s, ok := in[k].(string); ok && s != "" {
			return Line{Kind: "tool", Text: name, Arg: clip(oneLine(s), 160)}
		}
	}
	return Line{Kind: "tool", Text: name}
}

// outLines shows the first few lines of a tool's output.
func outLines(out string) []Line {
	all := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var lines []Line
	for i, l := range all {
		if i == 3 {
			lines = append(lines, Line{Kind: "out", Text: fmt.Sprintf("… +%d lines", len(all)-3)})
			break
		}
		if l != "" {
			lines = append(lines, Line{Kind: "out", Text: clip(l, 200)})
		}
	}
	return lines
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(n-1, 0)]) + "…"
}
