package opencode

import (
	"regexp"
	"testing"

	"github.com/n-delic/agent-watch/cli"
	"github.com/n-delic/agent-watch/cli/clitest"
)

// Real output of `opencode run --format json` for: cat note.txt, write out.txt, reply "finished".
func TestParse(t *testing.T) {
	got, sum := clitest.Parse(t, CLI{}, "testdata/opencode.jsonl")
	clitest.Shows(t, got, []cli.Line{{Kind: "tool", Text: "shell", Arg: "cat note.txt"}, {Kind: "out", Text: "hello"},
		{Kind: "tool", Text: "write", Arg: "/work/oc/out.txt"}, {Kind: "text", Text: "finished"}})
	if sum.Cost <= 0 || sum.In <= 0 || sum.Out <= 0 {
		t.Errorf("totals %+v, want cost and tokens > 0", sum)
	}
}

// opencode's errors show their message, not the escaped response body that repeats it.
func TestParseError(t *testing.T) {
	for raw, want := range map[string]string{
		`{"type":"error","error":{"type":"provider.auth","message":"free tier only in OpenCode","status":403,"response":{"body":"{\"x\":1}"}}}`: "provider.auth: free tier only in OpenCode",
		`{"type":"error","error":{"type":"unknown","message":"Transport: socket closed"}}`:                                                      "Transport: socket closed",
		`{"type":"error","error":{"name":"APIError","data":{"message":"rate limited"}}}`:                                                        "APIError: rate limited",
	} {
		if lines, _ := parse([]byte(raw)); len(lines) != 1 || lines[0].Kind != "err" || lines[0].Text != want {
			t.Errorf("parse(%s) = %+v, want err %q", raw, lines, want)
		}
	}
}

func TestNewSession(t *testing.T) {
	if s := (CLI{}).NewSession(); !regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`).MatchString(s) {
		t.Errorf("session %q is not in opencode's shape", s)
	}
}

// Another account is its own database, run standalone; flags go after "run".
func TestAccount(t *testing.T) {
	_, args, env := CLI{}.Command(cli.Turn{Account: "/data/work.db", Session: "ses_x", Prompt: "hi"})
	if args[0] != "run" || args[1] != "--standalone" || len(env) != 1 || env[0] != "OPENCODE_DB=/data/work.db" {
		t.Errorf("args %v env %v", args, env)
	}
}
