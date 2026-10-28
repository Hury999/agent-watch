// Package claude runs Claude Code (`claude -p`) as an agent.
package claude

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/n-delic/agent-watch/cli"
)

type CLI struct{ cli.Base }

func (CLI) Name() string { return "claude" }

// NewSession is a v4 UUID, which claude wants for --session-id.
func (CLI) NewSession() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Accounts are other config dirs, by convention ~/.claude-<name>, used as CLAUDE_CONFIG_DIR.
func (CLI) Accounts() ([]string, bool) { return cli.Dirs("~/.claude-*"), true }

func (CLI) Usage() string { return "limits" } // its rate_limit_event, in every turn's output

var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// shortcut: a fixed list, since claude has no way to list models; update it when new models come out.
var models = []cli.Model{
	{ID: "sonnet", Price: "latest Sonnet", Variants: efforts}, {ID: "opus", Price: "latest Opus", Variants: efforts},
	{ID: "haiku", Price: "latest Haiku", Variants: efforts}, {ID: "fable", Price: "latest Fable", Variants: efforts},
	{ID: "claude-fable-5-1", Price: "", Variants: efforts}, {ID: "claude-opus-5-5", Price: "", Variants: efforts},
	{ID: "claude-sonnet-5-5", Price: "", Variants: efforts}, {ID: "claude-haiku-5-5", Price: "", Variants: efforts},
}

func (CLI) Models(string) ([]cli.Model, error) { return models, nil }

func (CLI) Command(t cli.Turn) (string, []string, []string) {
	var env []string
	if t.Account != "" {
		env = []string{"CLAUDE_CONFIG_DIR=" + t.Account}
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	if t.Model != "" {
		args = append(args, "--model", t.Model)
	}
	if t.Effort != "" {
		args = append(args, "--effort", t.Effort)
	}
	if t.First {
		args = append(args, "--session-id", t.Session)
	} else {
		args = append(args, "--resume", t.Session)
	}
	if t.System != "" {
		tools := "Bash Read Grep Glob" // no edit tools unless the user allows it: the lead must not fix its agents' code
		if t.Edits {
			tools += " Edit Write"
		}
		// --append-system-prompt right after the tool list: the list takes several values and would swallow what follows
		args = append(args, "--allowedTools", tools, "--append-system-prompt", t.System)
	} else {
		args = append(args, "--permission-mode", "bypassPermissions")
	}
	return "claude", append(args, withImages(t.Prompt, t.Images)), env
}

// withImages tells claude, which has no attach flag, where to look.
func withImages(prompt string, images []string) string {
	if len(images) == 0 {
		return prompt
	}
	return prompt + "\n\n(The message includes image file(s); look at them with the Read tool: " + strings.Join(images, ", ") + ")"
}

func (CLI) NewParser() cli.Parser { return parse }

func parse(raw []byte) ([]cli.Line, cli.Delta) {
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
		return []cli.Line{{Kind: "info", Text: string(raw)}}, cli.Delta{}
	}
	switch e.Type {
	case "result":
		u := e.Usage
		d := cli.Delta{Cost: e.Cost, In: u.Input + u.CacheCreation + u.CacheRead, Out: u.Output}
		if e.IsError {
			return []cli.Line{{Kind: "err", Text: e.Result}}, d
		}
		return nil, d
	case "rate_limit_event":
		w := e.RateLimitInfo.UnifiedWindows
		return nil, cli.Delta{Limits: &cli.Limits{Windows: []cli.Window{
			{Label: "5h", Used: w.FiveHour.Utilization, Reset: w.FiveHour.ResetsAt},
			{Label: "7d", Used: w.SevenDay.Utilization, Reset: w.SevenDay.ResetsAt}}}}
	case "assistant", "user":
	default:
		return nil, cli.Delta{}
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
	var lines []cli.Line
	for _, b := range blocks {
		switch b.Type {
		case "text":
			lines = append(lines, cli.Line{Kind: "text", Text: strings.TrimSpace(b.Text)})
		case "thinking":
			if t := strings.TrimSpace(b.Thinking); t != "" {
				lines = append(lines, cli.Line{Kind: "think", Text: t})
			}
		case "tool_use":
			lines = append(lines, cli.ToolLine(b.Name, b.Input))
		case "tool_result":
			out := resultText(b.Content)
			if b.IsError {
				lines = append(lines, cli.Line{Kind: "err", Text: cli.Clip(out, 300)})
			} else {
				lines = append(lines, cli.OutLines(out)...)
			}
		}
	}
	return lines, cli.Delta{}
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
