package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

// Real output of `opencode run --format json` and `claude -p --output-format stream-json` for the same task:
// cat note.txt, write out.txt, reply "finished".
func TestParse(t *testing.T) {
	for kind, want := range map[string][]Line{
		"opencode": {{Kind: "tool", Text: "shell", Arg: "cat note.txt"}, {Kind: "out", Text: "hello"},
			{Kind: "tool", Text: "write", Arg: "/work/oc/out.txt"}, {Kind: "text", Text: "finished"}},
		"claude": {{Kind: "tool", Text: "Bash", Arg: "cat note.txt"}, {Kind: "tool", Text: "Write", Arg: "/work/cl/out.txt"},
			{Kind: "out", Text: "hello"}, {Kind: "text", Text: "finished"}},
	} {
		f, err := os.Open("testdata/" + kind + ".jsonl")
		if err != nil {
			t.Fatal(err)
		}
		var got []Line
		var sum Delta
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			lines, d := parse(kind, sc.Bytes())
			got = append(got, lines...)
			sum.Cost, sum.In, sum.Out = sum.Cost+d.Cost, sum.In+d.In, sum.Out+d.Out
			if d.Limits != nil {
				sum.Limits = d.Limits
			}
		}
		f.Close()
		i := 0 // want must appear in order; a text only has to start with the wanted text
		for _, l := range got {
			if i < len(want) && l.Kind == want[i].Kind && strings.HasPrefix(l.Text, want[i].Text) && l.Arg == want[i].Arg {
				i++
			}
		}
		if i < len(want) {
			t.Errorf("%s: missing %+v in %+v", kind, want[i], got)
		}
		if sum.Cost <= 0 || sum.In <= 0 || sum.Out <= 0 {
			t.Errorf("%s: totals %+v, want cost and tokens > 0", kind, sum)
		}
		if kind == "claude" && (sum.Limits == nil || sum.Limits.Five != 0.42 || sum.Limits.Seven != 0.52 || sum.Limits.FiveReset == 0) {
			t.Errorf("claude limits %+v, want 5h 0.42 and 7d 0.52 with reset times", sum.Limits)
		}
	}
}

// Every block renders within the width, whatever the kind, and markdown loses its markup.
func TestRender(t *testing.T) {
	lines := []Line{{Kind: "you", Text: "do it"}, {Kind: "text", Text: "## Summary\n- **bold** and `code`"},
		{Kind: "tool", Text: "Bash", Arg: strings.Repeat("x", 300)}, {Kind: "out", Text: "OK"}, {Kind: "err", Text: "bad"},
		{Kind: "think", Text: "hmm"}, {Kind: "done", Text: "turn 1 done"}, {Kind: "fail", Text: "turn 2 failed"}, {Kind: "info", Text: "note"}}
	out := render(lines, 40)
	for _, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > 40 {
			t.Errorf("line is %d wide: %q", w, l)
		}
	}
	if plain := xansi.Strip(out); strings.Contains(plain, "**") || strings.Contains(plain, "## ") {
		t.Errorf("markdown markup left in: %s", plain)
	}
}

func TestNewSession(t *testing.T) {
	if s := newSession("claude"); !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(s) {
		t.Errorf("claude session %q is not a v4 UUID", s)
	}
	if s := newSession("opencode-personal"); !strings.HasPrefix(s, "ses") {
		t.Errorf("opencode session %q must start with ses", s)
	}
}

func TestCommand(t *testing.T) {
	t.Setenv("PWD", "/somewhere/else")
	for _, tc := range []struct {
		a       Agent
		env     string // must be set
		arg     string // must be passed
		without string // must not be passed
	}{
		{Agent{Name: "claude", CLI: "claude"}, "PWD=/work/tree", "--session-id", "--model"},
		{Agent{Name: "opencode", CLI: "opencode", Model: "p/m", Effort: "max"}, "PWD=/work/tree", "p/m#max", "--standalone"},
		{Agent{Name: "work", CLI: "opencode", Data: "/data/work.db"}, "OPENCODE_DB=/data/work.db", "--standalone", "--model"},
		{Agent{Name: "claude-2", CLI: "claude", Data: "/cfg/two", Model: "opus"}, "CLAUDE_CONFIG_DIR=/cfg/two", "opus", "--standalone"},
	} {
		cmd := command(tc.a, newSession(tc.a.CLI), true, "hi", "", "/work/tree")
		env := cmd.Environ()
		if !slices.Contains(env, tc.env) || slices.Contains(env, "PWD=/somewhere/else") {
			t.Errorf("%s: env lacks %s or kept a stale PWD", tc.a.Name, tc.env)
		}
		if cmd.Args[0] != tc.a.CLI || !slices.Contains(cmd.Args, tc.arg) || slices.Contains(cmd.Args, tc.without) {
			t.Errorf("%s: args %v, want %s and not %s", tc.a.Name, cmd.Args, tc.arg, tc.without)
		}
		// the subcommand comes first: opencode ignores flags before "run" and prints its help
		if want := map[string]string{"claude": "-p", "opencode": "run"}[tc.a.CLI]; cmd.Args[1] != want {
			t.Errorf("%s: args %v, want %q right after the CLI", tc.a.Name, cmd.Args, want)
		}
	}
}

func TestConfigCheck(t *testing.T) {
	ok := Config{Lead: Lead{Agent: "claude"}, Agents: []Agent{{Name: "claude", CLI: "claude"}, {Name: "work", CLI: "opencode"}}}
	if e := ok.check(); e != "" {
		t.Errorf("valid config: %s", e)
	}
	for _, bad := range []Config{
		{Lead: Lead{Agent: "gone"}, Agents: ok.Agents},
		{Lead: Lead{Agent: "claude"}, Agents: append(slices.Clone(ok.Agents), Agent{Name: "work", CLI: "claude"})},
		{Lead: Lead{Agent: "claude"}, Agents: append(slices.Clone(ok.Agents), Agent{Name: "Bad Name", CLI: "claude"})},
	} {
		if bad.check() == "" {
			t.Errorf("%+v passed the check", bad)
		}
	}
	if l := ok.lead(); l.Name != "claude" || l.CLI != "claude" {
		t.Errorf("lead() = %+v", l)
	}
}

func TestDescribe(t *testing.T) {
	for a, want := range map[Agent]string{
		{Name: "work", CLI: "opencode", Model: "opencode-go/x", Effort: "max"}: "work opencode-go/x#max",
		{Name: "claude", CLI: "claude", Model: "sonnet", Effort: "high"}:       "claude sonnet high",
		{Name: "opencode", CLI: "opencode"}:                                    "opencode default model",
	} {
		if got := describe(a); got != want {
			t.Errorf("describe(%v) = %q, want %q", a, got, want)
		}
	}
}

// A saved run comes back with its sessions, transcripts and totals, and is found from its directory.
func TestSaveAndResume(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runDir := filepath.Join(stateDir(), "20260101-120000")
	os.MkdirAll(runDir, 0o755)
	lead := &agentState{name: "lead", dir: "/repo", session: "uuid-1", cfg: Agent{Name: "claude", CLI: "claude"}, turns: 2,
		lines: []Line{{Kind: "you", Text: "add search"}}, cost: 0.5, in: 100, out: 20}
	worker := &agentState{name: "search", dir: "/repo-aw-search", session: "ses_x", cfg: Agent{Name: "work", CLI: "opencode", Data: "~/w.db"},
		turns: 1, running: true, lines: []Line{{Kind: "tool", Text: "edit", Arg: "todo.py"}}}
	m := &model{cwd: "/repo", runDir: runDir, lead: lead, workers: []*agentState{worker}, notes: []string{"note"}, queue: []string{"queued"}}
	m.saveRun()

	if runs := runsIn("/repo"); len(runs) != 1 || runs[0] != runDir {
		t.Fatalf("runsIn = %v, want [%s]", runs, runDir)
	}
	if runs := runsIn("/elsewhere"); len(runs) != 0 {
		t.Errorf("runsIn(/elsewhere) = %v, want none", runs)
	}
	r, err := loadRun(runDir)
	if err != nil {
		t.Fatal(err)
	}
	l, w := r.Lead.restore(), r.Agents[0].restore()
	if l.session != "uuid-1" || l.turns != 2 || l.cost != 0.5 || l.lines[0].Text != "add search" || l.running {
		t.Errorf("lead restored as %+v", l)
	}
	if w.session != "ses_x" || w.cfg.Data != "~/w.db" || w.lines[0].Arg != "todo.py" || w.running || w.stopReason == "" {
		t.Errorf("agent restored as %+v; a turn cut off by closing must show as interrupted", w)
	}
	if !slices.Equal(r.Notes, []string{"note", "queued"}) {
		t.Errorf("notes %v, want the lead's untold note and queued message", r.Notes)
	}
}
