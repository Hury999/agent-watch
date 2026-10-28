package main

// Usage shown in the header, whatever each CLI reports (cli.CLI's Usage): limit windows, spend, or else
// the tokens this run used.

import (
	"fmt"
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

// fetchSpend asks a CLI for an account's spend today and over the last 7 days, in the background.
func fetchSpend(a Agent) tea.Cmd {
	return func() tea.Msg {
		today, err1 := cliOf(a).Spend(expand(a.Data), 0)
		week, err2 := cliOf(a).Spend(expand(a.Data), 7)
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
		if cliOf(a).Usage() == "spend" {
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

// accountUsage is what's known of an account's usage: its CLI's limit windows (or a quota it hit), its spend,
// or else the tokens this run used on it.
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
	switch cliOf(a).Usage() {
	case "spend":
		if s, ok := m.spend[key]; ok {
			return sDim.Render("today ") + sBold.Render(fmt.Sprintf("$%.2f", s.today)) + sDim.Render(" · 7d ") + sBold.Render(fmt.Sprintf("$%.2f", s.week))
		}
		return sDim.Render("…")
	case "limits":
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

// afterTurn fetches what a CLI only reports once a turn has ended: its spend, or its limits (Codex), in the background.
func afterTurn(a *agentState) tea.Cmd {
	c, acct, session := cliOf(a.cfg), expand(a.cfg.Data), a.session
	if c.Usage() == "spend" {
		return fetchSpend(a.cfg)
	}
	return func() tea.Msg { return limitsMsg{accountKey(a.cfg), c.Limits(acct, session)} }
}
