package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/n-delic/agent-watch/cli"
	"github.com/n-delic/agent-watch/cli/clitest"
)

// Real output of `codex exec --json` for: cat note.txt, write out.txt, reply "finished".
func TestParse(t *testing.T) {
	got, sum := clitest.Parse(t, CLI{}, "testdata/codex.jsonl")
	clitest.Shows(t, got, []cli.Line{{Kind: "tool", Text: "shell", Arg: "cat note.txt"}, {Kind: "out", Text: "hello"},
		{Kind: "tool", Text: "shell", Arg: `printf 'done' > out.txt`}, {Kind: "text", Text: "finished"}})
	if sum.In <= 0 || sum.Out <= 0 { // a subscription: no cost
		t.Errorf("totals %+v, want tokens > 0", sum)
	}
	if sum.Session == "" {
		t.Errorf("no session id picked up, so its next turn couldn't resume")
	}
}

// Codex keeps its limits in its session file, not in its output.
func TestLimits(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "10", "25")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "rollout-2026-10-25T14-59-14-th1.jsonl"), []byte(
		`{"type":"session_meta","payload":{"id":"th1"}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":5.0,"window_minutes":300,"resets_at":1795517620},"secondary":{"used_percent":40.0,"window_minutes":43200,"resets_at":1795600000}}}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":7.5,"window_minutes":300,"resets_at":1795517620},"secondary":null}}}`+"\n"), 0o644)
	l := CLI{}.Limits(home, "th1")
	if l == nil || len(l.Windows) != 1 || l.Windows[0] != (cli.Window{Label: "5h", Used: 0.075, Reset: 1795517620}) {
		t.Errorf("limits %+v, want the last report: 5h at 7.5%%", l)
	}
	if (CLI{}).Limits(home, "nope") != nil {
		t.Errorf("limits for a session that has no file")
	}
	if windowLabel(10080) != "7d" || windowLabel(43200) != "30d" || windowLabel(300) != "5h" {
		t.Errorf("window labels: %s %s %s", windowLabel(10080), windowLabel(43200), windowLabel(300))
	}
}
