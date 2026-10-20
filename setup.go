package main

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type setupForm struct {
	cfg Config
	row int
}

var efforts = []string{"", "low", "medium", "high", "xhigh", "max"}

// field is one setup row: text is edited by typing or cycled through choices, toggle is switched.
type field struct {
	label   string
	text    *string
	choices []string
	toggle  *bool
}

func (f *setupForm) fields() []field {
	c := &f.cfg
	fs := []field{
		{label: "  agent", text: &c.Master.Agent, choices: kinds},
		{label: "  model", text: &c.Master.Model},
		{label: "  effort", text: &c.Master.Effort, choices: efforts},
	}
	for i := range c.Slaves {
		s := &c.Slaves[i]
		fs = append(fs,
			field{label: "Slave " + s.Agent, toggle: &s.Enabled},
			field{label: "  model", text: &s.Model},
			field{label: "  effort", text: &s.Effort, choices: efforts})
	}
	return fs
}

func (m *model) setupKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.setup
	fs := f.fields()
	cur := fs[f.row]
	switch k.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "up":
		f.row = max(f.row-1, 0)
	case "down", "tab":
		f.row = min(f.row+1, len(fs)-1)
	case "esc":
		m.setup = nil
	case "enter":
		if f.cfg.Master != m.cfg.Master {
			if m.master.running {
				m.status = "the master is mid-turn; change it when it's done"
				return m, nil
			}
			m.master = &agentState{name: "master", dir: m.cwd, cfg: f.cfg.Master, session: newSession(f.cfg.Master.Agent),
				lines: []Line{{Kind: "info", Text: "new master session: " + describe(f.cfg.Master)}}}
		}
		m.cfg = f.cfg
		if err := saveConfig(m.cfg); err != nil {
			m.status = "could not save the config: " + err.Error()
		}
		m.setup = nil
		m.layout()
	case "left", "right", " ":
		switch {
		case cur.toggle != nil:
			*cur.toggle = !*cur.toggle
		case cur.choices != nil:
			step := 1
			if k.String() == "left" {
				step = len(cur.choices) - 1
			}
			*cur.text = cur.choices[(slices.Index(cur.choices, *cur.text)+step)%len(cur.choices)]
			if f.row == 0 { // a new master kind gets that kind's default model and effort
				f.cfg.Master = defaults(*cur.text)
			}
		case k.String() == " ":
			*cur.text += " "
		}
	case "backspace":
		if cur.text != nil && cur.choices == nil && *cur.text != "" {
			r := []rune(*cur.text)
			*cur.text = string(r[:len(r)-1])
		}
	default:
		if cur.text != nil && cur.choices == nil && k.Type == tea.KeyRunes {
			*cur.text += string(k.Runes)
		}
	}
	return m, nil
}

func (f *setupForm) view(w, h int) string {
	var b strings.Builder
	for i, fl := range f.fields() {
		var v string
		switch {
		case fl.toggle != nil && *fl.toggle:
			v = sGreen.Render("● enabled")
		case fl.toggle != nil:
			v = sDim.Render("○ disabled")
		case fl.choices != nil:
			c := *fl.text
			if c == "" {
				c = "default"
			}
			v = sDim.Render("‹ ") + sBold.Render(c) + sDim.Render(" ›")
			if f.row == 0 && i == 0 || slices.Contains(kinds, c) {
				v = sDim.Render("‹ ") + badge(c) + sDim.Render(" ›")
			}
		default:
			v = sBold.Render(*fl.text)
		}
		label := sDim.Render(fmt.Sprintf("%-24s", fl.label))
		if fl.toggle != nil {
			label = badge(strings.TrimPrefix(fl.label, "Slave ")) + sDim.Render(strings.Repeat(" ", max(24-len(fl.label)+6, 1)))
		}
		line := "  " + label + v
		if i == f.row {
			line = sAccent.Render("› ") + label + v
			if fl.text != nil && fl.choices == nil {
				line += sAccent.Render("▏")
			}
			line = lipgloss.NewStyle().Background(cSelBg).Width(min(w, 90) - 4).Render(line)
		}
		b.WriteString(line + "\n")
		if i == 2 {
			b.WriteString("\n" + sBold.Render("Slaves") + sDim.Render(" · the agents the master may start") + "\n")
		}
	}
	b.WriteString("\n" + sDim.Render("claude models: sonnet, opus, haiku, fable or a full id\nopencode models: provider/model, see `opencode models`"))
	keys := sAccent.Render("↑↓") + sDim.Render(" move  ") + sAccent.Render("←→ space") + sDim.Render(" change  ") +
		sAccent.Render("type") + sDim.Render(" edit model  ") + sAccent.Render("enter") + sDim.Render(" save  ") + sAccent.Render("esc") + sDim.Render(" cancel")
	pw := min(w, 90)
	box := panel(sAccent.Render("✻ ")+sBold.Render("Setup")+sDim.Render(" · who leads, who works"), sBold.Render("Master")+"\n"+b.String(), pw, min(h-2, 26), true)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box+"\n"+keys)
}
