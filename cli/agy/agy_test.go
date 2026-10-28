package agy

import (
	"testing"
	"time"

	"github.com/n-delic/agent-watch/cli"
	"github.com/n-delic/agent-watch/cli/clitest"
)

// Real output of `agy -p --output-format stream-json` for: cat note.txt, write out.txt, reply "finished".
func TestParse(t *testing.T) {
	got, sum := clitest.Parse(t, CLI{}, "testdata/agy.jsonl")
	clitest.Shows(t, got, []cli.Line{{Kind: "tool", Text: "run_command", Arg: "cat note.txt"}, {Kind: "out", Text: "hello"},
		{Kind: "tool", Text: "write_file", Arg: "/work/out.txt"}, {Kind: "text", Text: "finished."}})
	if sum.In <= 0 || sum.Out <= 0 { // a subscription: no cost
		t.Errorf("totals %+v, want tokens > 0", sum)
	}
	if sum.Session == "" {
		t.Errorf("no session id picked up, so its next turn couldn't resume")
	}
}

// agy only reports usage when its quota runs out.
func TestQuota(t *testing.T) {
	raw := `{"event":"result","result":{"status":"ERROR","error":"Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 14h36m37s."}}`
	lines, d := CLI{}.NewParser()([]byte(raw))
	if len(lines) != 1 || lines[0].Kind != "err" || d.Limits == nil || d.Limits.Windows[0].Label != "quota" || d.Limits.Windows[0].Used != 1 {
		t.Fatalf("quota error: %+v %+v", lines, d.Limits)
	}
	if left := time.Until(time.Unix(d.Limits.Windows[0].Reset, 0)); left < 14*time.Hour || left > 15*time.Hour {
		t.Errorf("quota resets in %s, want about 14h36m", left)
	}
}

// agy has one login: the setup offers no other account.
func TestNoAccounts(t *testing.T) {
	if _, ok := (CLI{}).Accounts(); ok {
		t.Error("agy claims it can use another account")
	}
}
