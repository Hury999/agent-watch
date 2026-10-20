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
	err string
}

var efforts = []string{"", "low", "medium", "high", "xhigh", "max"}

// field is one setup row: text is typed or cycled through choices, toggle is switched, action runs on enter/space.
type field struct {
	label   string
	text    *string
	empty   string // shown for an empty text
	choices []string
	toggle  *bool
	action  func()
	agent   int // index of the agent this row belongs to, -1 for none
	head    bool
}

func (f *setupForm) fields() []field {
	c := &f.cfg
	var names []string
	for _, a := range c.Agents {
		names = append(names, a.Name)
	}
	fs := []field{
		{label: "agent", text: &c.Lead.Agent, choices: names, agent: -1},
		{label: "model", text: &c.Lead.Model, empty: "default", agent: -1},
		{label: "effort", text: &c.Lead.Effort, choices: efforts, agent: -1},
	}
	for i := range c.Agents {
		a := &c.Agents[i]
		fs = append(fs,
			field{label: "name", text: &a.Name, agent: i, head: true},
			field{label: "cli", text: &a.CLI, choices: clis, agent: i},
			field{label: "account", text: &a.Data, empty: "default", agent: i},
			field{label: "model", text: &a.Model, empty: "default", agent: i},
			field{label: "effort", text: &a.Effort, choices: efforts, agent: i},
			field{label: "lead may use it", toggle: &a.Enabled, agent: i})
	}
	return append(fs, field{label: "+ add an agent", agent: -1, action: func() {
		n := len(c.Agents) + 1
		for _, ok := c.find(fmt.Sprint("agent-", n)); ok; _, ok = c.find(fmt.Sprint("agent-", n)) {
			n++
		}
		c.Agents = append(c.Agents, Agent{Name: fmt.Sprint("agent-", n), CLI: "opencode", Enabled: true})
		f.row = len(f.fields()) - 7 // its name row
	}})
}

func (m *model) setupKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.setup
	fs := f.fields()
	cur := fs[f.row]
	f.err = ""
	editable := cur.text != nil && cur.choices == nil
	switch k.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "up", "shift+tab":
		f.row = max(f.row-1, 0)
	case "down", "tab":
		f.row = min(f.row+1, len(fs)-1)
	case "esc":
		m.setup = nil
	case "ctrl+d":
		if cur.agent >= 0 && len(f.cfg.Agents) > 1 {
			f.cfg.Agents = slices.Delete(f.cfg.Agents, cur.agent, cur.agent+1)
			f.row = min(f.row, len(f.fields())-1)
		}
	case "enter":
		if cur.action != nil {
			cur.action()
			break
		}
		if f.err = f.cfg.check(); f.err != "" {
			break
		}
		if f.cfg.lead() != m.cfg.lead() {
			if m.lead.running {
				f.err = "the lead is mid-turn; change it when it's done"
				break
			}
			m.lead = &agentState{name: "lead", dir: m.cwd, cfg: f.cfg.lead(), session: newSession(f.cfg.lead().CLI),
				lines: []Line{{Kind: "info", Text: "new lead session: " + describe(f.cfg.lead())}}}
		}
		m.cfg = f.cfg
		setColors(m.cfg)
		if err := saveConfig(m.cfg); err != nil {
			m.status = "could not save the config: " + err.Error()
		}
		m.setup = nil
		m.layout()
		return m, m.refreshSpend()
	case "left", "right", " ":
		switch {
		case cur.action != nil:
			cur.action()
		case cur.toggle != nil:
			*cur.toggle = !*cur.toggle
		case cur.choices != nil:
			step := 1
			if k.String() == "left" {
				step = len(cur.choices) - 1
			}
			*cur.text = cur.choices[(slices.Index(cur.choices, *cur.text)+step)%len(cur.choices)]
		case editable && k.String() == " ":
			f.edit(cur, *cur.text+" ")
		}
	case "backspace":
		if editable && *cur.text != "" {
			r := []rune(*cur.text)
			f.edit(cur, string(r[:len(r)-1]))
		}
	default:
		if editable && k.Type == tea.KeyRunes {
			f.edit(cur, *cur.text+string(k.Runes))
		}
	}
	setColors(f.cfg)
	return m, nil
}

// edit changes a text field; renaming the lead's agent keeps the lead pointing at it.
func (f *setupForm) edit(fl field, v string) {
	if fl.head && f.cfg.Lead.Agent == *fl.text {
		f.cfg.Lead.Agent = v
	}
	*fl.text = v
}

func (f *setupForm) view(w, h int) string {
	pw := min(w, 96)
	var lines []string
	cursor := 0
	for i, fl := range f.fields() {
		if i == 0 {
			lines = append(lines, sBold.Render("Lead")+sDim.Render(" · the agent you talk to"))
		}
		if i == 3 {
			lines = append(lines, "", sBold.Render("Agents")+sDim.Render(" · who the lead can put to work"))
		}
		if fl.head {
			lines = append(lines, "")
		}
		var v string
		switch {
		case fl.action != nil:
			v = ""
		case fl.toggle != nil && *fl.toggle:
			v = sGreen.Render("● yes")
		case fl.toggle != nil:
			v = sDim.Render("○ no")
		case fl.choices != nil:
			c := *fl.text
			if c == "" {
				c = "default"
			}
			label := sBold.Render(c)
			if i == 0 {
				label = badge(c)
			}
			v = sDim.Render("‹ ") + label + sDim.Render(" ›")
		case *fl.text == "":
			v = sDim.Render(fl.empty)
		case fl.head:
			v = lipgloss.NewStyle().Bold(true).Foreground(agentColors[*fl.text]).Render(*fl.text)
		default:
			v = sBold.Render(*fl.text)
		}
		label := sDim.Render(fmt.Sprintf("  %-18s", fl.label))
		if fl.action != nil {
			label = sAccent.Render("  " + fl.label)
		}
		line := " " + label + v
		if i == f.row {
			cursor = len(lines)
			line = sAccent.Render("›") + label + v
			if fl.text != nil && fl.choices == nil {
				line += sAccent.Render("▏")
			}
			line = lipgloss.NewStyle().Background(cSelBg).Width(pw - 4).Render(line)
		}
		lines = append(lines, line)
	}
	help := []string{"", sDim.Render("empty model or account = the CLI's default · opencode models: `opencode models`"),
		sDim.Render("another account: opencode → its .db file, claude → its config dir (~/.claude-work)")}
	room := max(h-4-len(help), 5) // panel borders, keys line and error line
	start := min(max(cursor-room/2, 0), max(len(lines)-room, 0))
	lines = lines[start:min(start+room, len(lines))]
	keys := sAccent.Render("↑↓") + sDim.Render(" move  ") + sAccent.Render("←→ space") + sDim.Render(" change  ") +
		sAccent.Render("type") + sDim.Render(" edit  ") + sAccent.Render("^d") + sDim.Render(" remove agent  ") +
		sAccent.Render("enter") + sDim.Render(" save  ") + sAccent.Render("esc") + sDim.Render(" cancel")
	if f.err != "" {
		keys = sRed.Render("✗ "+f.err) + "\n" + keys
	}
	body := lipgloss.NewStyle().Width(pw - 4).Render(strings.Join(append(lines, help...), "\n"))
	box := panel(sAccent.Render("✻ ")+sBold.Render("Setup")+sDim.Render(" · who leads, who works"), body, pw, lipgloss.Height(body)+2, true)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box+"\n"+keys)
}
