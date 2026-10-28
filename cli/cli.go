// Package cli is the template every agent CLI follows: the command for one turn, its output turned into
// transcript lines, and what else agent-watch asks of it (sessions, accounts, models, usage). Each CLI
// lives in its own package under cli/ and embeds Base for what it doesn't do.
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Line is one displayed block of a transcript.
type Line struct {
	Kind string // you, text, think, tool (Text is the tool, Arg its argument), out, err, info, done, fail
	Text string
	Arg  string
	R    string `json:"-"` // rendered block, cached for width RW by the app
	RW   int    `json:"-"`
}

// Delta is what one output line adds to an agent's totals.
type Delta struct {
	Cost    float64
	In, Out int64
	Limits  *Limits // the account's usage windows, when the CLI reports them
	Session string  // a CLI that names its own sessions: the id it assigned, to resume it next turn
}

// Limits are an account's usage windows, as its CLI reports them.
type Limits struct{ Windows []Window }

// Window is one usage window: how much of it is used (0..1) and when it resets (unix seconds, 0 if unknown).
type Window struct {
	Label string // 5h, 7d, 30d, or quota
	Used  float64
	Reset int64
}

// Model is one entry of the setup's model picker.
type Model struct {
	ID       string
	Price    string   // "free", "$in/$out" per million tokens, or a note
	Variants []string // the model's effort levels
}

// Turn is what one turn of an agent needs.
type Turn struct {
	Model, Effort string
	Account       string // another login (a path, ~ expanded); "" is the CLI's default one
	Session       string // "" on the first turn of a CLI that names its own sessions
	First         bool   // the conversation's first turn
	Prompt        string
	System        string   // the lead's instructions; non-empty marks the lead
	Edits         bool     // the lead may edit files
	Images        []string // image files the prompt names
}

// Parser reads one turn's output, a line at a time.
type Parser func(raw []byte) ([]Line, Delta)

// CLI is one agent CLI agent-watch can run.
type CLI interface {
	Name() string
	// Command is one turn: the program, its arguments (the prompt last) and what it adds to the environment.
	// It runs in the turn's workdir, with no stdin.
	Command(t Turn) (prog string, args, env []string)
	// NewParser reads one turn's output; make one per turn.
	NewParser() Parser
	// Efforts are the reasoning efforts it takes when its model's own aren't known; "" first, its default.
	Efforts() []string
	// NewSession makes the id of a new conversation; "" if the CLI names its own (Delta.Session).
	NewSession() string
	// Accounts lists the other logins found on this machine, for the setup to offer; ok is false if the CLI
	// can't use another account at all.
	Accounts() (found []string, ok bool)
	// Models lists an account's models for the setup's picker; none means the picker takes what's typed.
	Models(account string) ([]Model, error)
	// Usage says what the header shows for an account: "limits" (windows from its output or Limits),
	// "spend" (Spend), or "" (only the tokens this run used).
	Usage() string
	// Spend is what an account spent over the last days (0: today).
	Spend(account string, days int) (float64, error)
	// Limits reads an account's usage windows once a turn has ended, for a CLI that leaves them out of its output.
	Limits(account, session string) *Limits
	// Interrupt stops a session the CLI could keep running after its process is killed.
	Interrupt(account, session string)
}

// Base is what a CLI does when it doesn't do something itself: embed it and override.
type Base struct{}

func (Base) Efforts() []string                      { return []string{"", "low", "medium", "high", "xhigh", "max"} }
func (Base) NewSession() string                     { return "" }
func (Base) Accounts() ([]string, bool)             { return nil, false }
func (Base) Models(account string) ([]Model, error) { return nil, nil }
func (Base) Usage() string                          { return "" }
func (Base) Spend(account string, days int) (float64, error) {
	return 0, errors.New("this cli reports no spend")
}
func (Base) Limits(account, session string) *Limits { return nil }
func (Base) Interrupt(account, session string)      {}

// WithSystem puts the lead's instructions in front of its first prompt, for a CLI without a system-prompt flag.
func WithSystem(system, prompt string) string {
	if system == "" {
		return prompt
	}
	return system + "\n\n---\n\n" + prompt
}

// ToolLine shows a tool call with the argument that says most about it.
func ToolLine(name string, in map[string]any) Line {
	for _, k := range []string{"command", "file_path", "filePath", "path", "pattern", "url", "query", "description", "CommandLine", "Path"} {
		if s, ok := in[k].(string); ok && s != "" {
			return Line{Kind: "tool", Text: name, Arg: Clip(OneLine(s), 160)}
		}
	}
	return Line{Kind: "tool", Text: name}
}

// OutLines shows the first few lines of a tool's output.
func OutLines(out string) []Line {
	all := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var lines []Line
	for i, l := range all {
		if i == 3 {
			lines = append(lines, Line{Kind: "out", Text: fmt.Sprintf("… +%d lines", len(all)-3)})
			break
		}
		if l != "" {
			lines = append(lines, Line{Kind: "out", Text: Clip(l, 200)})
		}
	}
	return lines
}

func OneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func Clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(n-1, 0)]) + "…"
}

// Expand resolves a leading ~ in a path.
func Expand(p string) string {
	if home, err := os.UserHomeDir(); err == nil && (p == "~" || strings.HasPrefix(p, "~/")) {
		return filepath.Join(home, p[1:])
	}
	return p
}

// Dirs lists the directories matching a glob, for Accounts.
func Dirs(glob string) []string {
	var dirs []string
	all, _ := filepath.Glob(Expand(glob))
	for _, d := range all {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}
