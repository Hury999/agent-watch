package claude

import (
	"regexp"
	"testing"

	"github.com/n-delic/agent-watch/cli"
	"github.com/n-delic/agent-watch/cli/clitest"
)

// Real output of `claude -p --output-format stream-json` for: cat note.txt, write out.txt, reply "finished".
func TestParse(t *testing.T) {
	got, sum := clitest.Parse(t, CLI{}, "testdata/claude.jsonl")
	clitest.Shows(t, got, []cli.Line{{Kind: "tool", Text: "Bash", Arg: "cat note.txt"}, {Kind: "tool", Text: "Write", Arg: "/work/cl/out.txt"},
		{Kind: "out", Text: "hello"}, {Kind: "text", Text: "finished"}})
	if sum.Cost <= 0 || sum.In <= 0 || sum.Out <= 0 {
		t.Errorf("totals %+v, want cost and tokens > 0", sum)
	}
	if l := sum.Limits; l == nil || len(l.Windows) != 2 || l.Windows[0] != (cli.Window{Label: "5h", Used: 0.42, Reset: l.Windows[0].Reset}) ||
		l.Windows[1].Label != "7d" || l.Windows[1].Used != 0.52 || l.Windows[0].Reset == 0 {
		t.Errorf("limits %+v, want 5h 0.42 and 7d 0.52 with reset times", l)
	}
}

func TestNewSession(t *testing.T) {
	if s := (CLI{}).NewSession(); !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(s) {
		t.Errorf("session %q is not a v4 UUID", s)
	}
}
