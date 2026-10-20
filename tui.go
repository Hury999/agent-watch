package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

type model struct {
	cfg              Config
	runDir, cwd, exe string
	master           *agentState
	slaves           []*agentState
	sel              int
	queue            []string    // messages for the master's next turn
	notes            []string    // told to the master along with its next message
	focusList        bool        // keys go to the slave list instead of the input
	target           *agentState // the slave the input is messaging (m); nil = master
	full             bool        // selected slave fullscreen
	quitArmed        bool
	setup            *setupForm
	input            textinput.Model
	mvp, svp         viewport.Model
	w, h             int
	status           string
	limits           *Limits // Claude's rate limit windows, from the latest claude turn
	spend            map[string]spend
	spendAt          time.Time
}

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func runTUI() error {
	cfg, saved := loadConfig()
	cwd, _ := os.Getwd()
	exe, _ := os.Executable()
	home, _ := os.UserHomeDir()
	// shortcut: every launch is a fresh run (no resuming the master or slaves), add resume if wanted.
	runDir := filepath.Join(home, ".local", "state", "agent-watch", time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Join(runDir, "requests"), 0o755); err != nil {
		return err
	}
	in := textinput.New()
	in.Prompt = sAccent.Render("> ")
	in.PlaceholderStyle = sDim
	in.Cursor.Style = sAccent
	in.Placeholder = "message the master…"
	in.Focus()
	m := &model{cfg: cfg, runDir: runDir, cwd: cwd, exe: exe, input: in, spend: map[string]spend{},
		master: &agentState{name: "master", dir: cwd, cfg: cfg.Master, session: newSession(cfg.Master.Agent)}}
	if !saved {
		m.setup = &setupForm{cfg: cfg}
	}
	prog = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := prog.Run()
	return err
}

func (m *model) Init() tea.Cmd { return tea.Batch(tick(), m.refreshSpend()) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
	case tickMsg:
		if m.pollRequests() {
			m.refresh()
		}
		if time.Since(m.spendAt) > 5*time.Minute {
			return m, tea.Batch(tick(), m.refreshSpend())
		}
		return m, tick()
	case linesMsg:
		a, d := msg.a, msg.d
		a.lines = append(a.lines, msg.lines...)
		a.cost, a.turnCost = a.cost+d.Cost, a.turnCost+d.Cost
		a.in, a.out, a.turnIn, a.turnOut = a.in+d.In, a.out+d.Out, a.turnIn+d.In, a.turnOut+d.Out
		if d.Limits != nil {
			m.limits = d.Limits
		}
		for _, l := range msg.lines {
			switch l.Kind {
			case "text":
				a.lastText = l.Text
			case "err":
				a.lastErr = l.Text
			}
		}
		m.refresh()
	case doneMsg:
		m.finished(msg.a, msg.code)
		m.refresh()
		if strings.HasPrefix(msg.a.cfg.Agent, "opencode") {
			return m, fetchSpend(msg.a.cfg.Agent)
		}
	case spendMsg:
		if msg.ok {
			m.spend[msg.kind] = msg.s
		}
	case stopMsg:
		msg.a.stop(msg.reason)
	case tea.MouseMsg:
		return m.mouse(msg)
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// turnSummary is "1m 12s · ↑ 23.4k ↓ 1.2k · $0.004" for the last turn.
func turnSummary(a *agentState) string {
	return fmt.Sprintf("%s · ↑ %s ↓ %s · $%.3f", dur(a.lastDur), tokens(a.turnIn), tokens(a.turnOut), a.turnCost)
}

func (m *model) finished(a *agentState, code int) {
	a.running, a.exit = false, code
	a.lastDur = time.Since(a.started)
	a.totalDur += a.lastDur
	if a.status() == "finished" {
		a.lines = append(a.lines, Line{Kind: "done", Text: fmt.Sprintf("turn %d done in %s", a.turns, turnSummary(a))})
	} else {
		a.lines = append(a.lines, Line{Kind: "fail", Text: fmt.Sprintf("turn %d %s · %s", a.turns, a.status(), turnSummary(a))})
	}
	if a == m.master {
		m.flushMaster()
		return
	}
	reply := clip(a.lastText, 8000)
	if reply == "" {
		reply = "(no reply)"
	}
	m.master.lines = append(m.master.lines, Line{Kind: "info", Text: fmt.Sprintf("slave %s %s turn %d in %s", a.name, a.status(), a.turns, dur(a.lastDur))})
	m.toMaster(fmt.Sprintf("[agent-watch] Slave %q (%s) %s on turn %d after %s.\nWorkdir: %s\nIts final reply:\n%s",
		a.name, describe(a.cfg), a.status(), a.turns, dur(a.lastDur), a.dir, reply))
}

func (m *model) toMaster(text string) {
	m.queue = append(m.queue, text)
	m.flushMaster()
}

// flushMaster starts the master's next turn with everything queued, unless it is busy.
func (m *model) flushMaster() {
	if m.master.running || len(m.queue) == 0 {
		return
	}
	msg := strings.Join(append(m.notes, m.queue...), "\n\n")
	m.notes, m.queue = nil, nil
	if err := m.master.start(msg, m.systemPrompt(), []string{"AGENT_WATCH_DIR=" + m.runDir}, m.logPath(m.master)); err != nil {
		m.master.lines = append(m.master.lines, Line{Kind: "fail", Text: "could not start the master: " + err.Error()})
	}
}

func (m *model) systemPrompt() string {
	var s []string
	for _, a := range m.cfg.Slaves {
		if a.Enabled {
			s = append(s, "- "+a.Agent+" (runs "+describe(a)+")")
		}
	}
	if len(s) == 0 {
		s = []string{"- none enabled: tell the user to enable slaves in the setup (ctrl+s)"}
	}
	return strings.NewReplacer("{{EXE}}", m.exe, "{{CWD}}", m.cwd, "{{SLAVES}}", strings.Join(s, "\n")).Replace(masterPrompt)
}

func (m *model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.setup != nil {
		return m.setupKey(k)
	}
	if k.String() != "ctrl+c" {
		m.quitArmed = false
	}
	m.status = ""
	switch k.String() {
	case "ctrl+c":
		if m.running() && !m.quitArmed {
			m.quitArmed = true
			m.status = "agents are running: ctrl+c again stops them and quits"
			return m, nil
		}
		for _, a := range append(m.slaves, m.master) {
			a.stop("stopped: agent-watch quit")
		}
		return m, tea.Quit
	case "ctrl+s":
		m.setup = &setupForm{cfg: m.cfg}
		return m, nil
	case "tab":
		m.setFocusList(!m.focusList)
		return m, nil
	case "esc":
		m.target, m.full = nil, false
		m.setFocusList(false)
		m.layout()
		return m, nil
	case "pgup", "pgdown":
		vp := &m.mvp
		if m.focusList || m.full {
			vp = &m.svp
		}
		var cmd tea.Cmd
		*vp, cmd = vp.Update(k)
		return m, cmd
	}
	if m.focusList {
		s := m.slaves[m.sel]
		switch k.String() {
		case "up", "k":
			m.selectSlave(m.sel - 1)
		case "down", "j":
			m.selectSlave(m.sel + 1)
		case "enter":
			m.full = !m.full
			m.layout()
		case "m":
			m.target = s
			m.setFocusList(false)
		case "x":
			if s.running {
				s.stop("stopped by the user")
			}
		}
		return m, nil
	}
	if k.Type == tea.KeyEnter {
		return m, m.submit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

// submit sends the input to the master, or to the slave being messaged.
func (m *model) submit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	if t := m.target; t != nil {
		if t.running {
			m.status = t.name + " is still running; message it when it's done"
			return nil
		}
		t.lines = append(t.lines, Line{Kind: "you", Text: text})
		if err := t.start(text, "", nil, m.logPath(t)); err != nil {
			m.status = "could not start " + t.name + ": " + err.Error()
			return nil
		}
		m.notes = append(m.notes, fmt.Sprintf("[agent-watch] The user sent slave %q this message directly:\n%s", t.name, text))
		m.target = nil
		m.setFocusList(false)
	} else {
		m.master.lines = append(m.master.lines, Line{Kind: "you", Text: text})
		if m.master.running {
			m.master.lines = append(m.master.lines, Line{Kind: "info", Text: "queued until the master's turn ends"})
		}
		m.toMaster(text)
	}
	m.input.SetValue("")
	m.refresh()
	return nil
}

func (m *model) setFocusList(on bool) {
	m.focusList = on && len(m.slaves) > 0
	m.input.Placeholder = "message the master…"
	if m.target != nil {
		m.input.Placeholder = "message slave " + m.target.name + " directly…  (esc cancels)"
	}
	if m.focusList {
		m.input.Blur()
	} else {
		m.input.Focus()
	}
}

func (m *model) selectSlave(i int) {
	if i < 0 || i >= len(m.slaves) || i == m.sel {
		return
	}
	m.sel = i
	m.svp.GotoBottom()
	m.refresh()
}

// geometry of the main screen, shared by View and mouse hit-testing.
func (m *model) bodyH() int    { return m.h - 5 } // header (1), input box (3), footer (1)
func (m *model) leftW() int    { return m.w * 55 / 100 }
func (m *model) listRows() int { return max(1, min(len(m.slaves), 8)) }
func (m *model) listH() int    { return m.listRows() + 2 }
func (m *model) listStart() int {
	return max(m.sel-m.listRows()+1, 0)
}

func (m *model) mouse(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.setup != nil {
		return m, nil
	}
	inLeft := !m.full && e.X < m.leftW() && e.Y >= 1 && e.Y <= m.bodyH()
	inList := !m.full && e.X >= m.leftW() && e.Y >= 1 && e.Y < 1+m.listH()
	inDetail := e.Y >= 1 && e.Y <= m.bodyH() && (m.full || e.X >= m.leftW() && e.Y >= 1+m.listH())
	switch {
	case e.Button == tea.MouseButtonWheelUp || e.Button == tea.MouseButtonWheelDown:
		vp := &m.mvp
		if inDetail || inList {
			vp = &m.svp
		}
		if e.Button == tea.MouseButtonWheelUp {
			vp.ScrollUp(3)
		} else {
			vp.ScrollDown(3)
		}
	case e.Action != tea.MouseActionPress || e.Button != tea.MouseButtonLeft:
	case inList:
		if i := m.listStart() + e.Y - 2; e.Y >= 2 && i < len(m.slaves) {
			if i == m.sel && m.focusList {
				m.full = true // a click on the selected slave opens it fullscreen
				m.layout()
			}
			m.selectSlave(i)
		}
		m.setFocusList(true)
	case m.full && e.Y == 1:
		m.full = false // the fullscreen title closes it
		m.layout()
	case inDetail:
		m.setFocusList(true)
	case inLeft || e.Y > m.bodyH():
		m.setFocusList(false)
	}
	return m, nil
}

func (m *model) running() bool {
	for _, a := range append(m.slaves, m.master) {
		if a.running {
			return true
		}
	}
	return false
}

func (m *model) find(name string) *agentState {
	for _, a := range m.slaves {
		if a.name == name {
			return a
		}
	}
	return nil
}

func (m *model) enabled() []string {
	var names []string
	for _, a := range m.cfg.Slaves {
		if a.Enabled {
			names = append(names, a.Agent)
		}
	}
	return names
}

func (m *model) logPath(a *agentState) string { return filepath.Join(m.runDir, a.name+".jsonl") }

func (m *model) layout() {
	lw := m.leftW()
	// panels: border (2) + padding (2) wide; border (2) + status line (1) high
	m.mvp.Width, m.mvp.Height = max(lw-4, 1), max(m.bodyH()-3, 1)
	if m.full {
		m.svp.Width, m.svp.Height = max(m.w-4, 1), max(m.bodyH()-3, 1)
	} else {
		m.svp.Width, m.svp.Height = max(m.w-lw-4, 1), max(m.bodyH()-m.listH()-3, 1)
	}
	m.input.Width = max(m.w-8, 1)
	m.refresh()
}

func (m *model) refresh() {
	setContent(&m.mvp, m.master.lines)
	if len(m.slaves) > 0 {
		setContent(&m.svp, m.slaves[m.sel].lines)
	}
}

// setContent re-renders a pane, staying at the bottom if it was there.
func setContent(vp *viewport.Model, lines []Line) {
	follow := vp.AtBottom()
	vp.SetContent(render(lines, vp.Width))
	if follow {
		vp.GotoBottom()
	}
}

func icon(a *agentState) string {
	switch {
	case a.running:
		return sAccent.Render(spinFrame())
	case a.turns == 0:
		return sDim.Render("○")
	case a.stopReason != "":
		return sYellow.Render("■")
	case a.exit != 0:
		return sRed.Render("✗")
	}
	return sGreen.Render("✓")
}

// statusLine is a pane's last line: the live spinner while running, the totals when idle.
func statusLine(a *agentState, idle string) string {
	if a.running {
		return sAccent.Render(spinFrame()) + " " + shimmer(a.verb+"…") +
			sDim.Render(fmt.Sprintf(" (%s · ↑ %s ↓ %s · $%.3f)", dur(time.Since(a.started)), tokens(a.turnIn), tokens(a.turnOut), a.turnCost))
	}
	if a.turns == 0 {
		return sDim.Render(idle)
	}
	turns := "turns"
	if a.turns == 1 {
		turns = "turn"
	}
	return icon(a) + sDim.Render(fmt.Sprintf(" %d %s · %s · ↑ %s ↓ %s · $%.3f", a.turns, turns, dur(a.totalDur), tokens(a.in), tokens(a.out), a.cost))
}

func agentTitle(a *agentState, name string) string {
	return name + sDim.Render(" · ") + badge(a.cfg.Agent) + " " + sDim.Render(strings.TrimPrefix(describe(a.cfg), a.cfg.Agent+" "))
}

func (m *model) slaveRows(w int) string {
	if len(m.slaves) == 0 {
		return sDim.Render("none yet · the master starts them")
	}
	var rows []string
	for i := m.listStart(); i < m.listStart()+m.listRows() && i < len(m.slaves); i++ {
		s := m.slaves[i]
		took := s.totalDur
		if s.running {
			took += time.Since(s.started)
		}
		model := s.cfg.Model[strings.LastIndex(s.cfg.Model, "/")+1:]
		row := icon(s) + " " +
			sBold.Render(fmt.Sprintf("%-12s", clip(s.name, 12))) + " " +
			lipgloss.NewStyle().Foreground(kindColor(s.cfg.Agent)).Render(fmt.Sprintf("%-17s", s.cfg.Agent)) + " " +
			sDim.Render(fmt.Sprintf("%7s  ↓%-6s $%.3f  %s", dur(took), tokens(s.out), s.cost, model))
		row = xansi.Truncate(row, w, "…")
		if i == m.sel {
			bg := lipgloss.NewStyle().Width(w)
			if m.focusList {
				bg = bg.Background(cSelBg)
			}
			row = bg.Render(row)
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

func (m *model) header() string {
	logo := lipgloss.NewStyle().Background(cAccent).Foreground(lipgloss.Color("#1A1A1A")).Bold(true).Render(" ✻ agent-watch ")
	home, _ := os.UserHomeDir()
	where := sDim.Render(" " + strings.Replace(m.cwd, home, "~", 1))
	usage := m.usageLine()
	gap := m.w - lipgloss.Width(logo) - lipgloss.Width(where) - lipgloss.Width(usage) - 1
	if gap < 1 {
		where, gap = "", max(m.w-lipgloss.Width(logo)-lipgloss.Width(usage)-1, 1)
	}
	return xansi.Truncate(logo+where+strings.Repeat(" ", gap)+usage, m.w, "…")
}

func (m *model) footer() string {
	if m.status != "" {
		return sYellow.Render(xansi.Truncate(" "+m.status, m.w, "…"))
	}
	keys := [][2]string{{"enter", "send"}, {"tab", "slaves"}, {"click", "select"}, {"m", "message slave"},
		{"x", "stop slave"}, {"⏎/click", "fullscreen"}, {"wheel", "scroll"}, {"esc", "back"}, {"^s", "setup"}, {"^c", "quit"}}
	var parts []string
	for _, k := range keys {
		parts = append(parts, sAccent.Render(k[0])+" "+sDim.Render(k[1]))
	}
	return xansi.Truncate(" "+strings.Join(parts, sDim.Render("  ·  ")), m.w, "…")
}

func (m *model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.setup != nil {
		return m.setup.view(m.w, m.h)
	}
	bodyH := m.bodyH()
	var body string
	if m.full {
		s := m.slaves[m.sel]
		t := icon(s) + " " + agentTitle(s, sBold.Render(s.name)) + sDim.Render(" · esc or click here to close")
		body = panel(t, m.svp.View()+"\n"+statusLine(s, ""), m.w, bodyH, true)
	} else {
		lw := m.leftW()
		mt := sAccent.Render("✻ ") + agentTitle(m.master, sBold.Render("Master"))
		left := panel(mt, m.mvp.View()+"\n"+statusLine(m.master, "type below to talk to the master"), lw, bodyH, !m.focusList)
		run, done := 0, 0
		for _, s := range m.slaves {
			if s.running {
				run++
			} else if s.turns > 0 {
				done++
			}
		}
		lt := sBold.Render("Slaves") + sDim.Render(fmt.Sprintf(" · %d running · %d done", run, done))
		list := panel(lt, m.slaveRows(m.w-lw-4), m.w-lw, m.listH(), m.focusList)
		detail := panel(sDim.Render("no slave selected"), "", m.w-lw, bodyH-m.listH(), false)
		if len(m.slaves) > 0 {
			s := m.slaves[m.sel]
			detail = panel(icon(s)+" "+agentTitle(s, sBold.Render(s.name)), m.svp.View()+"\n"+statusLine(s, ""), m.w-lw, bodyH-m.listH(), m.focusList)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, lipgloss.JoinVertical(lipgloss.Left, list, detail))
	}
	ib := cBorder
	if !m.focusList {
		ib = cAccent
	}
	input := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ib).Padding(0, 1).Width(m.w - 2).Render(m.input.View())
	return lipgloss.JoinVertical(lipgloss.Left, m.header(), body, input, m.footer())
}
