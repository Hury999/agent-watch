package main

// Runs are saved in their run dir so you can go back: `aw -c` continues the latest run in a directory,
// `aw -r` picks one. Resuming reattaches to the same agent sessions, so their conversations go on.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type savedAgent struct {
	Name, Dir, Session string
	Cfg                Agent
	Turns, Exit        int
	Lines              []Line
	Cost               float64
	In, Out            int64
	TotalDur           time.Duration
	StopReason         string
	Running            bool
}

type savedRun struct {
	Cwd    string
	Saved  time.Time
	Lead   savedAgent
	Agents []savedAgent
	Notes  []string // what the lead hadn't been told yet
}

func save(a *agentState) savedAgent {
	return savedAgent{a.name, a.dir, a.session, a.cfg, a.turns, a.exit, a.lines, a.cost, a.in, a.out, a.totalDur, a.stopReason, a.running}
}

func (s savedAgent) restore() *agentState {
	a := &agentState{name: s.Name, dir: s.Dir, session: s.Session, cfg: s.Cfg, turns: s.Turns, exit: s.Exit, lines: s.Lines,
		cost: s.Cost, in: s.In, out: s.Out, totalDur: s.TotalDur, stopReason: s.StopReason}
	if s.Running {
		a.stopReason = "interrupted: agent-watch closed mid-turn"
	}
	return a
}

// saveRun writes the run's state, replacing the last one in one rename so a crash never leaves half a file.
func (m *model) saveRun() {
	r := savedRun{Cwd: m.cwd, Saved: time.Now(), Lead: save(m.lead), Notes: append(append([]string(nil), m.notes...), m.queue...)}
	for _, a := range m.workers {
		r.Agents = append(r.Agents, save(a))
	}
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	tmp := filepath.Join(m.runDir, "state.json.tmp")
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, filepath.Join(m.runDir, "state.json"))
	}
	m.dirty, m.savedAt = false, time.Now()
}

func stateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "agent-watch")
}

func loadRun(runDir string) (savedRun, error) {
	var r savedRun
	b, err := os.ReadFile(filepath.Join(runDir, "state.json"))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

// runsIn lists the saved runs started in dir, newest first.
func runsIn(dir string) []string {
	all, _ := filepath.Glob(filepath.Join(stateDir(), "*", "state.json"))
	var runs []string
	saved := map[string]time.Time{}
	for _, f := range all {
		if r, err := loadRun(filepath.Dir(f)); err == nil && r.Cwd == dir {
			runs = append(runs, filepath.Dir(f))
			saved[filepath.Dir(f)] = r.Saved
		}
	}
	sort.Slice(runs, func(i, j int) bool { return saved[runs[i]].After(saved[runs[j]]) })
	return runs
}

// pickRun lists this directory's runs and reads the user's choice; enter takes the newest.
func pickRun(dir string) (string, error) {
	runs := runsIn(dir)
	if len(runs) == 0 {
		return "", fmt.Errorf("no saved runs in %s", dir)
	}
	for i, run := range runs {
		r, _ := loadRun(run)
		first := ""
		for _, l := range r.Lead.Lines {
			if l.Kind == "you" {
				first = oneLine(l.Text)
				break
			}
		}
		fmt.Printf("%s %s  %s  %s\n", sAccent.Render(fmt.Sprintf("%2d", i+1)), r.Saved.Format("Jan 02 15:04"),
			sDim.Render(fmt.Sprintf("%d agents · lead %d turns", len(r.Agents), r.Lead.Turns)), clip(first, 70))
	}
	fmt.Print("\nresume which run? [1] ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return runs[0], nil
	}
	n, err := strconv.Atoi(answer)
	if err != nil || n < 1 || n > len(runs) {
		return "", fmt.Errorf("no run %q", answer)
	}
	return runs[n-1], nil
}
