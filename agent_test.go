package main

import (
	"bufio"
	"os"
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
		"opencode-decomp": {{Kind: "tool", Text: "shell", Arg: "cat note.txt"}, {Kind: "out", Text: "hello"},
			{Kind: "tool", Text: "write", Arg: "/work/oc/out.txt"}, {Kind: "text", Text: "finished"}},
		"claude": {{Kind: "tool", Text: "Bash", Arg: "cat note.txt"}, {Kind: "tool", Text: "Write", Arg: "/work/cl/out.txt"},
			{Kind: "out", Text: "hello"}, {Kind: "text", Text: "finished"}},
	} {
		f, err := os.Open("testdata/" + strings.SplitN(kind, "-", 2)[0] + ".jsonl")
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

func TestCommandPWD(t *testing.T) {
	t.Setenv("PWD", "/somewhere/else")
	for _, kind := range kinds {
		cmd := command(defaults(kind), newSession(kind), true, "hi", "", "/work/tree")
		if pwd := cmd.Environ(); !slices.Contains(pwd, "PWD=/work/tree") || slices.Contains(pwd, "PWD=/somewhere/else") {
			t.Errorf("%s: env PWD is not the workdir: %v", kind, pwd)
		}
	}
}

func TestDescribe(t *testing.T) {
	for a, want := range map[Agent]string{
		{Agent: "opencode-decomp", Model: "opencode-go/x", Effort: "max"}: "opencode-decomp opencode-go/x#max",
		{Agent: "claude", Model: "sonnet", Effort: "high"}:                "claude sonnet high",
		{Agent: "claude", Model: "haiku"}:                                 "claude haiku",
	} {
		if got := describe(a); got != want {
			t.Errorf("describe(%v) = %q, want %q", a, got, want)
		}
	}
}
