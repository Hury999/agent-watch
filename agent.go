package main

// One turn of an agent CLI: the command that runs it, and its JSON output turned into display lines.

import (
	"cmp"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
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
	Session string  // codex and agy: the session id the CLI assigned, to resume it next turn
}

// stream is what parsing one turn remembers between lines: agy sends its text in pieces.
type stream struct {
	texts map[int]string // agy: text so far, per step
	tools map[int]bool   // agy: steps whose tool call was shown
}

// Limits is how much of Claude's 5-hour and 7-day windows is used (0..1) and when each resets (unix seconds).
type Limits struct {
	Five, Seven           float64
	FiveReset, SevenReset int64
}

// newSession makes a session id: claude wants a UUID, opencode its own shape, "ses_" + 12 hex + 14 base62.
// opencode's free tier refuses any other shape with a 403 FreeTierError.
func newSession(cli string) string {
	if cli == "codex" || cli == "agy" {
		return "" // they make their own; parse picks it up from the first turn (Delta.Session)
	}
	b := make([]byte, 16)
	rand.Read(b)
	if cli == "claude" {
		b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	}
	// The hex is opencode's descending timestamp, so its session list sorts ours like its own.
	t := ^uint64(time.Now().UnixMilli() * 0x1000)
	const b62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	r := make([]byte, 14)
	for i := range r {
		r[i] = b62[int(b[i])%len(b62)]
	}
	return fmt.Sprintf("ses_%012x%s", t&(1<<48-1), r)
}

// account points a CLI at a non-default account: env vars, and for opencode --standalone,
// because a separate database can't go through the shared background service.
func account(a Agent) (env, args []string) {
	switch {
	case a.Data == "":
		return nil, nil
	case a.CLI == "claude":
		return []string{"CLAUDE_CONFIG_DIR=" + expand(a.Data)}, nil
	case a.CLI == "codex":
		return []string{"CODEX_HOME=" + expand(a.Data)}, nil
	}
	return []string{"OPENCODE_DB=" + expand(a.Data)}, []string{"--standalone"}
}

// command builds one turn. A non-empty system prompt marks the lead; edits says whether it may edit files.
func command(a Agent, session string, first bool, prompt, system, dir string, edits bool) *exec.Cmd {
	if strings.HasPrefix(prompt, "-") {
		prompt = " " + prompt // or the CLI reads it as a flag
	}
	images := imagePaths(prompt)
	env, args := account(a)
	if a.CLI == "claude" {
		prompt = withImages(prompt, images)
		args = append(args, "-p", "--output-format", "stream-json", "--verbose")
		if a.Model != "" {
			args = append(args, "--model", a.Model)
		}
		if a.Effort != "" {
			args = append(args, "--effort", a.Effort)
		}
		if first {
			args = append(args, "--session-id", session)
		} else {
			args = append(args, "--resume", session)
		}
		if system != "" {
			tools := "Bash Read Grep Glob" // no edit tools unless the user allows it: the lead must not fix its agents' code
			if edits {
				tools += " Edit Write"
			}
			args = append(args, "--allowedTools", tools, "--append-system-prompt", system)
		} else {
			args = append(args, "--permission-mode", "bypassPermissions")
		}
	} else if a.CLI == "codex" {
		// shortcut: codex can't keep a lead from editing and still let it run git and aw, so its no-edit rule is
		// only in its instructions; give the lead the sandbox's read-only mode if codex ever allows writable roots there.
		args = append(args, "exec")
		if session != "" {
			args = append(args, "resume")
		}
		args = append(args, "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox")
		for _, img := range images {
			args = append(args, "-i", img)
		}
		if a.Model != "" {
			args = append(args, "-m", a.Model)
		}
		if slices.Contains(cliEfforts["codex"], a.Effort) && a.Effort != "" { // a known word only: it goes into TOML
			args = append(args, "-c", `model_reasoning_effort="`+a.Effort+`"`)
		}
		if session != "" {
			args = append(args, session)
		} else if system != "" {
			prompt = system + "\n\n---\n\n" + prompt
		}
	} else if a.CLI == "agy" {
		// agy can't be kept from editing either (its plan mode runs no commands at all, so a lead couldn't work).
		args = append(args, "--output-format", "stream-json", "--dangerously-skip-permissions")
		if a.Model != "" {
			args = append(args, "--model", a.Model)
		}
		if slices.Contains(cliEfforts["agy"], a.Effort) && a.Effort != "" {
			args = append(args, "--effort", a.Effort)
		}
		if session != "" {
			args = append(args, "--conversation", session)
		} else if system != "" {
			prompt = system + "\n\n---\n\n" + prompt
		}
		args = append(args, "-p") // the prompt follows it; agy has no flag for images, so their paths stay in the prompt
	} else {
		args = append(append([]string{"run"}, args...), "--session", session, "--auto", "--format", "json") // flags only work after "run"
		for _, img := range images {
			args = append(args, "--file", img)
		}
		if a.Model != "" {
			model := a.Model
			if a.Effort != "" {
				model += "#" + a.Effort
			}
			args = append(args, "--model", model)
		}
		if system != "" {
			if !edits {
				args = append(args, "--agent", "plan") // opencode's agent that can't edit files
			}
			if first {
				prompt = system + "\n\n---\n\n" + prompt
			}
		}
	}
	cmd := exec.Command(a.CLI, append(args, prompt)...)
	// stdin stays nil (/dev/null): opencode waits forever on an open non-tty stdin.
	cmd.Dir = dir
	// opencode takes its directory from $PWD, which an explicit Env would leave stale.
	cmd.Env = append(append(os.Environ(), env...), "PWD="+dir)
	ownGroup(cmd) // so stop can kill the whole tree
	return cmd
}

// interrupt stops a session server-side. Only opencode's shared background service needs it:
// it keeps running a session after its client is killed. Standalone servers die with the client.
func interrupt(a Agent, session string) {
	if a.CLI == "opencode" && a.Data == "" {
		exec.Command("opencode", "api", "session.interrupt", "--param", "sessionID="+session).Run()
	}
}

// parse turns one line of an agent's JSON output into display lines and what it adds to the totals.
// st carries what a turn's earlier lines left behind; start one per turn.
func parse(cli string, raw []byte, st *stream) ([]Line, Delta) {
	if strings.TrimSpace(string(raw)) == "" {
		return nil, Delta{}
	}
	switch cli {
	case "claude":
		return parseClaude(raw)
	case "codex":
		return parseCodex(raw)
	case "agy":
		return parseAgy(raw, st)
	}
	return parseOpencode(raw)
}

// parseCodex reads `codex exec --json`: a thread id, then items as they start and complete, and usage per turn.
func parseCodex(raw []byte) ([]Line, Delta) {
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
		return []Line{{Kind: "info", Text: string(raw)}}, Delta{}
	}
	it := e.Item
	switch e.Type {
	case "thread.started":
		return nil, Delta{Session: e.ThreadID}
	case "turn.completed":
		return nil, Delta{In: e.Usage.Input, Out: e.Usage.Output + e.Usage.Reasoning}
	case "turn.failed":
		return []Line{{Kind: "err", Text: e.Error.Message}}, Delta{}
	case "error":
		return []Line{{Kind: "err", Text: e.Message}}, Delta{}
	case "item.started":
		switch it.Type {
		case "command_execution":
			return []Line{toolLine("shell", map[string]any{"command": unwrapShell(it.Command)})}, Delta{}
		case "mcp_tool_call":
			return []Line{{Kind: "tool", Text: it.Tool}}, Delta{}
		}
	case "item.completed":
		switch it.Type {
		case "agent_message":
			return []Line{{Kind: "text", Text: strings.TrimSpace(it.Text)}}, Delta{}
		case "reasoning":
			if t := strings.TrimSpace(it.Text); t != "" {
				return []Line{{Kind: "think", Text: t}}, Delta{}
			}
		case "command_execution":
			lines := outLines(it.Output)
			if it.ExitCode != nil && *it.ExitCode != 0 {
				lines = append(lines, Line{Kind: "err", Text: fmt.Sprintf("exit code %d", *it.ExitCode)})
			}
			return lines, Delta{}
		case "file_change":
			var lines []Line
			for _, c := range it.Changes {
				lines = append(lines, Line{Kind: "tool", Text: cmp.Or(c.Kind, "edit"), Arg: c.Path})
			}
			return lines, Delta{}
		case "error":
			return []Line{{Kind: "err", Text: it.Message}}, Delta{}
		}
	}
	return nil, Delta{}
}

// unwrapShell shows `/bin/bash -lc 'cat x'` as `cat x`.
func unwrapShell(c string) string {
	if rest, ok := strings.CutPrefix(c, "/bin/bash -lc "); ok && len(rest) >= 2 && (rest[0] == '\'' || rest[0] == '"') && rest[len(rest)-1] == rest[0] {
		return rest[1 : len(rest)-1]
	}
	return c
}

// parseAgy reads agy's stream-json: an init with the conversation id, then step updates. Text arrives in pieces,
// gathered per step until it's DONE; usage comes per step (the result's covers the whole conversation).
func parseAgy(raw []byte, st *stream) ([]Line, Delta) {
	var e struct {
		Event          string
		ConversationID string `json:"conversation_id"`
		Message        string
		StepUpdate     struct {
			StepIndex int    `json:"step_index"`
			State     string `json:"state"`
			StepType  string `json:"step_type"`
			TextDelta string `json:"text_delta"`
			ToolName  string `json:"tool_name"`
			ToolInfo  struct {
				Name       string
				Parameters map[string]any
				Output     string
			} `json:"tool_info"`
			Usage struct {
				Input     int64 `json:"input_tokens"`
				Output    int64 `json:"output_tokens"`
				Thinking  int64 `json:"thinking_tokens"`
				CacheRead int64 `json:"cache_read_tokens"`
			}
		} `json:"step_update"`
		Result struct{ Status, Error string }
	}
	if json.Unmarshal(raw, &e) != nil {
		return []Line{{Kind: "info", Text: string(raw)}}, Delta{}
	}
	switch e.Event {
	case "init":
		return nil, Delta{Session: e.ConversationID}
	case "result":
		if e.Result.Status != "SUCCESS" {
			return []Line{{Kind: "err", Text: cmp.Or(e.Result.Error, "agy: "+e.Result.Status)}}, Delta{}
		}
		return nil, Delta{}
	case "error":
		return []Line{{Kind: "err", Text: e.Message}}, Delta{}
	case "step_update":
	default:
		return nil, Delta{}
	}
	if st.texts == nil {
		st.texts, st.tools = map[int]string{}, map[int]bool{}
	}
	s, done := e.StepUpdate, e.StepUpdate.State == "DONE"
	var d Delta
	if done {
		d = Delta{In: s.Usage.Input + s.Usage.CacheRead, Out: s.Usage.Output + s.Usage.Thinking}
	}
	var lines []Line
	switch s.StepType {
	case "agent_response", "planner_response", "thinking":
		st.texts[s.StepIndex] += s.TextDelta
		if t := strings.TrimSpace(st.texts[s.StepIndex]); done && t != "" {
			delete(st.texts, s.StepIndex)
			kind := "text"
			if s.StepType == "thinking" {
				kind = "think"
			}
			lines = append(lines, Line{Kind: kind, Text: t})
		}
	case "tool":
		if !st.tools[s.StepIndex] {
			st.tools[s.StepIndex] = true
			lines = append(lines, toolLine(cmp.Or(s.ToolInfo.Name, s.ToolName), s.ToolInfo.Parameters))
		}
		if done {
			out := strings.ReplaceAll(s.ToolInfo.Output, "\r\n", "\n")
			out, _ = strings.CutPrefix(out, "Command exited with code 0.\n\nCommand output:\n")
			lines = append(lines, outLines(out)...)
		}
	}
	return lines, d
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
		Error struct {
			Type, Name, Message string
			Data                struct{ Message string }
		}
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
		// Just the message: the full error repeats it inside an escaped response body.
		er := e.Error
		msg := cmp.Or(er.Message, er.Data.Message, string(raw))
		if kind := cmp.Or(er.Type, er.Name); kind != "" && kind != "unknown" {
			msg = kind + ": " + msg
		}
		return []Line{{Kind: "err", Text: msg}}, Delta{}
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
	for _, k := range []string{"command", "file_path", "filePath", "path", "pattern", "url", "query", "description", "CommandLine", "Path"} {
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

// relative shows paths in and next to dir as Claude Code does: "x.go", "../repo-aw-x/x.go".
func relative(s, dir string) string {
	if dir == "" || dir == "/" {
		return s
	}
	s = strings.ReplaceAll(s, dir+"/", "")
	if parent := filepath.Dir(dir); parent != "/" {
		s = strings.ReplaceAll(s, parent+"/", "../")
	}
	return s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(n-1, 0)]) + "…"
}
