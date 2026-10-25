package main

// Usage shown in the header: Claude's rate limit windows come with its output (parse);
// opencode has no limits API, so for it we show spend from `opencode stats`.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type spend struct {
	today, week float64
}

type spendMsg struct {
	account string // accountKey
	s       spend
	ok      bool
}

func opencodeCost(a Agent, days int) (float64, error) {
	env, args := account(a)
	cmd := exec.Command("opencode", append([]string{"stats", "--json", "--days", fmt.Sprint(days)}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	var s struct{ Cost float64 }
	return s.Cost, json.Unmarshal(out, &s)
}

// fetchSpend asks opencode for an account's spend today and over the last 7 days, in the background.
func fetchSpend(a Agent) tea.Cmd {
	return func() tea.Msg {
		today, err1 := opencodeCost(a, 0)
		week, err2 := opencodeCost(a, 7)
		return spendMsg{accountKey(a), spend{today, week}, err1 == nil && err2 == nil}
	}
}

// usedAccounts are the logins the current setup can use, once each: the enabled agents' and the lead's.
// Each is labeled with the first agent on it, or "lead".
func (m *model) usedAccounts() []Agent {
	var used []Agent
	seen := map[string]bool{}
	add := func(a Agent) {
		if !seen[accountKey(a)] {
			seen[accountKey(a)] = true
			used = append(used, a)
		}
	}
	for _, a := range m.cfg.Agents {
		if a.Enabled {
			add(a)
		}
	}
	lead := m.cfg.lead()
	lead.Name = "lead"
	add(lead)
	return used
}

func (m *model) refreshSpend() tea.Cmd {
	m.spendAt = time.Now()
	var cmds []tea.Cmd
	for _, a := range m.usedAccounts() {
		if a.CLI == "opencode" {
			cmds = append(cmds, fetchSpend(a))
		}
	}
	return tea.Batch(cmds...)
}

// usageLine is the header's right side: the lead's account always, and the selected agent's when it's another one.
func (m *model) usageLine() string {
	parts := []string{sAccent.Render("lead") + " " + m.accountUsage(m.lead.cfg)}
	if len(m.workers) > 0 {
		if w := m.workers[m.sel].cfg; accountKey(w) != accountKey(m.lead.cfg) {
			parts = append(parts, badge(w.Name)+" "+m.accountUsage(w))
		}
	}
	return strings.Join(parts, sDim.Render("  │  "))
}

// accountUsage is what's known of an account's usage: its CLI's limit windows (Claude, Codex, an agy quota hit),
// opencode's spend, or else (agy) the tokens this run used on it.
func (m *model) accountUsage(a Agent) string {
	key := accountKey(a)
	if l := m.limits[key]; l != nil && len(l.Windows) > 0 {
		var ws []string
		for i, w := range l.Windows {
			s := sDim.Render(w.Label+" ") + meter(w.Used)
			if i == 0 && w.Reset > 0 {
				s += sDim.Render(" ↻" + dur(time.Until(time.Unix(w.Reset, 0)).Round(time.Minute)))
			}
			ws = append(ws, s)
		}
		return strings.Join(ws, "  ")
	}
	switch a.CLI {
	case "opencode":
		if s, ok := m.spend[key]; ok {
			return sDim.Render("today ") + sBold.Render(fmt.Sprintf("$%.2f", s.today)) + sDim.Render(" · 7d ") + sBold.Render(fmt.Sprintf("$%.2f", s.week))
		}
		return sDim.Render("…")
	case "claude", "codex":
		return sDim.Render("limits after its first turn")
	}
	var in, out int64
	for _, s := range append([]*agentState{m.lead}, m.workers...) {
		if accountKey(s.cfg) == key {
			in, out = in+s.in, out+s.out
		}
	}
	return sDim.Render(fmt.Sprintf("↑ %s ↓ %s this run", tokens(in), tokens(out)))
}

type limitsMsg struct {
	account string // accountKey
	l       *Limits
}

// fetchCodexLimits reads a Codex session's limits once its turn has ended, in the background.
func fetchCodexLimits(a Agent, session string) tea.Cmd {
	return func() tea.Msg { return limitsMsg{accountKey(a), codexLimits(a, session)} }
}
