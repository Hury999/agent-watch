// Package clitest feeds a CLI's recorded output through its parser, for the CLIs' tests.
package clitest

import (
	"bufio"
	"cmp"
	"os"
	"strings"
	"testing"

	"github.com/n-delic/agent-watch/cli"
)

// Parse runs a file of real output through one turn's parser: the lines it shows and its summed totals.
func Parse(t *testing.T, c cli.CLI, file string) ([]cli.Line, cli.Delta) {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	parse := c.NewParser()
	var got []cli.Line
	var sum cli.Delta
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		lines, d := parse(sc.Bytes())
		got = append(got, lines...)
		sum.Cost, sum.In, sum.Out = sum.Cost+d.Cost, sum.In+d.In, sum.Out+d.Out
		sum.Session = cmp.Or(d.Session, sum.Session)
		if d.Limits != nil {
			sum.Limits = d.Limits
		}
	}
	return got, sum
}

// Shows fails unless want appears in got in order; a line's text only has to start with the wanted text.
func Shows(t *testing.T, got, want []cli.Line) {
	t.Helper()
	i := 0
	for _, l := range got {
		if i < len(want) && l.Kind == want[i].Kind && strings.HasPrefix(l.Text, want[i].Text) && l.Arg == want[i].Arg {
			i++
		}
	}
	if i < len(want) {
		t.Errorf("missing %+v in %+v", want[i], got)
	}
}
