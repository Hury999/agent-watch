package main

// Running agents, and the request files through which the lead's `agent-watch spawn|send|stop` reach the TUI.

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

//go:embed lead.md
var leadPrompt string

const turnTimeout = 45 * time.Minute

var prog *tea.Program

type agentState struct {
	name, dir, session string
	cfg                Agent
	turns              int
	lines              []Line
	cost               float64
	cmd                *exec.Cmd
	running            bool
	stopReason         string
	lastText, lastErr  string
	exit               int
	verb               string        // the spinner's word for this turn
	started            time.Time     // when this turn started
	lastDur, totalDur  time.Duration // last turn's and all turns' run time
	in, out            int64         // tokens over all turns
	turnIn, turnOut    int64
	turnCost           float64
}

type linesMsg struct {
	a     *agentState
	lines []Line
	d     Delta
}
type doneMsg struct {
	a    *agentState
	code int
}
type stopMsg struct {
	a      *agentState
	reason string
}

// start runs one turn in the background. Its output arrives as linesMsg, its end as doneMsg,
// and the raw output is appended to logPath.
func (a *agentState) start(prompt, system string, env []string, logPath string) error {
	cmd := command(a.cfg, a.session, a.turns == 0, prompt, system, a.dir)
	cmd.Env = append(cmd.Env, env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	a.cmd, a.running, a.stopReason, a.lastText, a.lastErr = cmd, true, "", "", ""
	a.turns++
	a.verb, a.started, a.turnIn, a.turnOut, a.turnCost = randomVerb(), time.Now(), 0, 0, 0
	timer := time.AfterFunc(turnTimeout, func() { prog.Send(stopMsg{a, "timed out after " + turnTimeout.String()}) })
	log, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	go func() {
		defer log.Close()
		errDone := make(chan struct{})
		go func() {
			sc := bufio.NewScanner(stderr)
			for sc.Scan() {
				fmt.Fprintln(log, sc.Text())
				if t := strings.TrimSpace(xansi.Strip(sc.Text())); t != "" {
					prog.Send(linesMsg{a, []Line{{Kind: "err", Text: t}}, Delta{}})
				}
			}
			close(errDone)
		}()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 64<<20)
		for sc.Scan() {
			fmt.Fprintf(log, "%s\n", sc.Bytes())
			if lines, d := parse(a.cfg.CLI, sc.Bytes()); len(lines) > 0 || d != (Delta{}) {
				prog.Send(linesMsg{a, lines, d})
			}
		}
		<-errDone
		err := cmd.Wait()
		timer.Stop()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			code = 1
		}
		prog.Send(doneMsg{a, code})
	}()
	return nil
}

func (a *agentState) stop(reason string) {
	if !a.running {
		return
	}
	a.stopReason = reason
	syscall.Kill(-a.cmd.Process.Pid, syscall.SIGTERM)
	interrupt(a.cfg, a.session)
}

// status says how the last turn ended.
func (a *agentState) status() string {
	switch {
	case a.running:
		return "running"
	case a.stopReason != "":
		return a.stopReason + " (interrupted, work may be partial)"
	case a.exit != 0:
		return fmt.Sprintf("failed with exit %d: %s", a.exit, a.lastErr)
	}
	return "finished"
}

type request struct {
	Op, Name, Agent, Dir, Prompt string
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// handle runs one request from the lead and returns the answer its command prints.
func (m *model) handle(r request) string {
	a := m.find(r.Name)
	switch r.Op {
	case "spawn":
		cfg, ok := m.cfg.enabled(r.Agent)
		switch {
		case !validName.MatchString(r.Name):
			return "error: the name must be lowercase letters, digits and dashes"
		case a != nil:
			return "error: agent " + r.Name + " already exists; use send for a follow-up, or pick a new name"
		case !ok:
			return "error: " + r.Agent + " is not an enabled agent. Enabled: " + strings.Join(m.enabled(), ", ")
		case !isDir(r.Dir):
			return "error: workdir is not a directory: " + r.Dir
		case strings.TrimSpace(r.Prompt) == "":
			return "error: the brief on stdin is empty"
		}
		a = &agentState{name: r.Name, dir: r.Dir, cfg: cfg, session: newSession(cfg.CLI)}
	case "send":
		switch {
		case a == nil:
			return "error: no agent named " + r.Name
		case a.running:
			return "error: " + r.Name + " is still running; wait for its [agent-watch] message"
		case strings.TrimSpace(r.Prompt) == "":
			return "error: the message on stdin is empty"
		}
	case "stop":
		if a == nil || !a.running {
			return "error: " + r.Name + " is not running"
		}
		a.stop("stopped by the lead")
		return "ok: stopping " + r.Name
	default:
		return "error: unknown request " + r.Op
	}
	a.lines = append(a.lines, Line{Kind: "you", Text: "from the lead:\n" + strings.TrimSpace(r.Prompt)})
	m.dirty = true
	if err := a.start(r.Prompt, "", nil, m.logPath(a)); err != nil {
		return "error: could not start " + r.Name + ": " + err.Error()
	}
	if r.Op == "spawn" {
		m.workers = append(m.workers, a)
		m.layout()
	}
	return fmt.Sprintf("ok: %s turn %d started on %s. You get an [agent-watch] message when it finishes; end your turn now instead of waiting.",
		a.name, a.turns, describe(a.cfg))
}

// pollRequests answers the request files the CLI dropped; it reports whether there were any.
func (m *model) pollRequests() bool {
	files, _ := filepath.Glob(filepath.Join(m.runDir, "requests", "*.json"))
	for _, f := range files {
		ans := "error: unreadable request"
		var r request
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &r) == nil {
			ans = m.handle(r)
		}
		os.WriteFile(strings.TrimSuffix(f, ".json")+".res", []byte(ans), 0o644)
		os.Remove(f)
	}
	return len(files) > 0
}

const usage = `usage: agent-watch                                open the TUI (see agent-watch -h)
       agent-watch spawn <name> <type> <workdir> < brief
       agent-watch send <name> < message
       agent-watch stop <name>`

// cli is the lead's side: it hands a request to the running TUI and prints the answer.
func cli(args []string) int {
	var r request
	switch {
	case len(args) == 4 && args[0] == "spawn":
		r = request{Op: "spawn", Name: args[1], Agent: args[2], Dir: args[3]}
	case len(args) == 2 && (args[0] == "send" || args[0] == "stop"):
		r = request{Op: args[0], Name: args[1]}
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	dir := os.Getenv("AGENT_WATCH_DIR")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "agent-watch: only the lead inside agent-watch can run this (AGENT_WATCH_DIR is not set)")
		return 2
	}
	if r.Op != "stop" {
		// Only read a pipe or file: an agent's open stdin would block forever.
		if st, err := os.Stdin.Stat(); err != nil || st.Mode()&os.ModeNamedPipe == 0 && !st.Mode().IsRegular() {
			fmt.Fprintln(os.Stderr, "agent-watch: pass the text on stdin, e.g. with a heredoc: <<'EOF' ... EOF")
			return 2
		}
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "agent-watch:", err)
			return 1
		}
		r.Prompt = string(b)
	}
	if r.Dir != "" {
		if abs, err := filepath.Abs(r.Dir); err == nil {
			r.Dir = abs
		}
	}
	id := filepath.Join(dir, "requests", strconv.FormatInt(time.Now().UnixNano(), 10))
	b, _ := json.Marshal(r)
	if err := os.WriteFile(id+".tmp", b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "agent-watch:", err)
		return 1
	}
	os.Rename(id+".tmp", id+".json") // the TUI only picks up complete files
	for range 100 {
		time.Sleep(100 * time.Millisecond)
		if ans, err := os.ReadFile(id + ".res"); err == nil {
			os.Remove(id + ".res")
			fmt.Println(string(ans))
			if strings.HasPrefix(string(ans), "ok") {
				return 0
			}
			return 1
		}
	}
	os.Remove(id + ".json")
	fmt.Fprintln(os.Stderr, "agent-watch: no answer from the TUI (is it still open?)")
	return 1
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
