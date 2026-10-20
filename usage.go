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

// usageLine is the header's right side: one group per account in use.
func (m *model) usageLine() string {
	var parts []string
	for _, a := range m.usedAccounts() {
		label := badge(a.Name) + " "
		if a.Name == "lead" {
			label = sAccent.Render("lead") + " "
		}
		key := accountKey(a)
		if a.CLI == "claude" {
			if l := m.limits[key]; l != nil {
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
		if s, ok := m.spend[key]; ok {
			parts = append(parts, label+sDim.Render("today ")+sBold.Render(fmt.Sprintf("$%.2f", s.today))+sDim.Render(" · 7d ")+sBold.Render(fmt.Sprintf("$%.2f", s.week)))
		} else {
			parts = append(parts, label+sDim.Render("…"))
		}
	}
	return strings.Join(parts, sDim.Render("  │  "))
}
