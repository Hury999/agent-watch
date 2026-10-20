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
	agent string
	s     spend
	ok    bool
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
		return spendMsg{a.Name, spend{today, week}, err1 == nil && err2 == nil}
	}
}

// usedAgents are the agents the current setup can run: the lead's and the enabled ones.
func (m *model) usedAgents() []Agent {
	var used []Agent
	for _, a := range m.cfg.Agents {
		if a.Enabled || a.Name == m.cfg.Lead.Agent {
			used = append(used, a)
		}
	}
	return used
}

func (m *model) refreshSpend() tea.Cmd {
	m.spendAt = time.Now()
	var cmds []tea.Cmd
	for _, a := range m.usedAgents() {
		if a.CLI == "opencode" {
			cmds = append(cmds, fetchSpend(a))
		}
	}
	return tea.Batch(cmds...)
}

// usageLine is the header's right side: one group per agent in use.
func (m *model) usageLine() string {
	var parts []string
	for _, a := range m.usedAgents() {
		label := badge(a.Name) + " "
		if a.CLI == "claude" {
			if l := m.limits[a.Name]; l != nil {
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
		if s, ok := m.spend[a.Name]; ok {
			parts = append(parts, label+sDim.Render("today ")+sBold.Render(fmt.Sprintf("$%.2f", s.today))+sDim.Render(" · 7d ")+sBold.Render(fmt.Sprintf("$%.2f", s.week)))
		} else {
			parts = append(parts, label+sDim.Render("…"))
		}
	}
	return strings.Join(parts, sDim.Render("  │  "))
}
