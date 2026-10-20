package main

// Usage shown in the header: Claude's rate limit windows come with its output (parse);
// opencode has no limits API, so for it we show spend from `opencode stats`.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type spend struct {
	today, week float64
}

type spendMsg struct {
	kind string
	s    spend
	ok   bool
}

func opencodeCost(kind string, days int) (float64, error) {
	cmd := exec.Command("opencode", "stats", "--json", "--days", fmt.Sprint(days))
	if kind == "opencode-personal" {
		home, _ := os.UserHomeDir()
		cmd.Env = append(os.Environ(), "OPENCODE_DB="+filepath.Join(home, ".local/share/opencode/personal.db"))
		cmd.Args = append(cmd.Args, "--standalone")
	}
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	var s struct{ Cost float64 }
	return s.Cost, json.Unmarshal(out, &s)
}

// fetchSpend asks opencode for an account's spend today and over the last 7 days, in the background.
func fetchSpend(kind string) tea.Cmd {
	return func() tea.Msg {
		today, err1 := opencodeCost(kind, 0)
		week, err2 := opencodeCost(kind, 7)
		return spendMsg{kind, spend{today, week}, err1 == nil && err2 == nil}
	}
}

// usedKinds are the agent kinds the current setup can run: the master and the enabled slaves.
func (m *model) usedKinds() []string {
	var ks []string
	for _, k := range kinds {
		if k == m.cfg.Master.Agent {
			ks = append(ks, k)
			continue
		}
		if _, ok := m.cfg.slave(k); ok {
			ks = append(ks, k)
		}
	}
	return ks
}

func (m *model) refreshSpend() tea.Cmd {
	m.spendAt = time.Now()
	var cmds []tea.Cmd
	for _, k := range m.usedKinds() {
		if strings.HasPrefix(k, "opencode") {
			cmds = append(cmds, fetchSpend(k))
		}
	}
	return tea.Batch(cmds...)
}

// usageLine is the header's right side: one meter group per agent kind in use.
func (m *model) usageLine() string {
	var parts []string
	for _, k := range m.usedKinds() {
		label := badge(k) + " "
		if k == "claude" {
			if l := m.limits; l != nil {
				reset := ""
				if l.FiveReset > 0 {
					reset = sDim.Render(" ↻" + dur(time.Until(time.Unix(l.FiveReset, 0)).Round(time.Minute)))
				}
				parts = append(parts, label+sDim.Render("5h ")+meter(l.Five)+reset+sDim.Render("  7d ")+meter(l.Seven))
			} else {
				parts = append(parts, label+sDim.Render("limits after its first turn"))
			}
			continue
		}
		if s, ok := m.spend[k]; ok {
			parts = append(parts, label+sDim.Render("today ")+sBold.Render(fmt.Sprintf("$%.2f", s.today))+sDim.Render(" · 7d ")+sBold.Render(fmt.Sprintf("$%.2f", s.week)))
		} else {
			parts = append(parts, label+sDim.Render("…"))
		}
	}
	return strings.Join(parts, sDim.Render("  │  "))
}
