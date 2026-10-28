// Package opencode runs opencode (`opencode run`) as an agent.
package opencode

import (
	"cmp"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/n-delic/agent-watch/cli"
)

type CLI struct{ cli.Base }

func (CLI) Name() string { return "opencode" }

// NewSession is in opencode's own shape, "ses_" + 12 hex + 14 base62: its free tier refuses any other
// with a 403 FreeTierError. The hex is its descending timestamp, so its session list sorts ours like its own.
func (CLI) NewSession() string {
	b := make([]byte, 14)
	rand.Read(b)
	t := ^uint64(time.Now().UnixMilli() * 0x1000)
	const b62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for i := range b {
		b[i] = b62[int(b[i])%len(b62)]
	}
	return fmt.Sprintf("ses_%012x%s", t&(1<<48-1), b)
}

const dataDir = "~/.local/share/opencode"

// Accounts are the other databases next to the default one, each its own login (OPENCODE_DB).
func (CLI) Accounts() ([]string, bool) {
	dbs, _ := filepath.Glob(filepath.Join(cli.Expand(dataDir), "*.db"))
	return slices.DeleteFunc(dbs, func(p string) bool { return filepath.Base(p) == "opencode.db" }), true
}

// account points opencode at another login. --standalone too: a separate database can't go through
// the shared background service. Its flags only work after the subcommand.
func account(acct string) (env, flags []string) {
	if acct == "" {
		return nil, nil
	}
	return []string{"OPENCODE_DB=" + acct}, []string{"--standalone"}
}

func (CLI) Command(t cli.Turn) (string, []string, []string) {
	env, flags := account(t.Account)
	args := append(append([]string{"run"}, flags...), "--session", t.Session, "--auto", "--format", "json")
	for _, img := range t.Images {
		args = append(args, "--file", img)
	}
	if t.Model != "" {
		model := t.Model
		if t.Effort != "" {
			model += "#" + t.Effort
		}
		args = append(args, "--model", model)
	}
	prompt := t.Prompt
	if t.System != "" {
		if !t.Edits {
			args = append(args, "--agent", "plan") // opencode's agent that can't edit files
		}
		if t.First {
			prompt = cli.WithSystem(t.System, prompt)
		}
	}
	return "opencode", append(args, prompt), env
}

// Interrupt stops a session on opencode's shared background service, which keeps running it after its
// client is killed. A standalone server (another account) dies with its client.
func (CLI) Interrupt(acct, session string) {
	if acct == "" {
		exec.Command("opencode", "api", "session.interrupt", "--param", "sessionID="+session).Run()
	}
}

func (CLI) Usage() string { return "spend" } // opencode has no limits API

func (CLI) Spend(acct string, days int) (float64, error) {
	env, flags := account(acct)
	cmd := exec.Command("opencode", append([]string{"stats", "--json", "--days", fmt.Sprint(days)}, flags...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	var s struct{ Cost float64 }
	return s.Cost, json.Unmarshal(out, &s)
}

// Models are the account's own, with prices and effort variants.
func (CLI) Models(acct string) ([]cli.Model, error) {
	env, flags := account(acct)
	cmd := exec.Command("opencode", append([]string{"api", "model.list"}, flags...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("opencode api model.list: %w", err)
	}
	var r struct {
		Data []struct {
			ID, ProviderID string
			Cost           []struct{ Input, Output float64 }
			Variants       []struct{ ID string }
		}
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	var list []cli.Model
	for _, d := range r.Data {
		mi := cli.Model{ID: d.ProviderID + "/" + d.ID, Price: "free"}
		if len(d.Cost) > 0 && (d.Cost[0].Input > 0 || d.Cost[0].Output > 0) {
			mi.Price = fmt.Sprintf("$%g/$%g", d.Cost[0].Input, d.Cost[0].Output)
		}
		for _, v := range d.Variants {
			mi.Variants = append(mi.Variants, v.ID)
		}
		list = append(list, mi)
	}
	slices.SortFunc(list, func(x, y cli.Model) int { return strings.Compare(x.ID, y.ID) })
	return list, nil
}

func (CLI) NewParser() cli.Parser { return parse }

func parse(raw []byte) ([]cli.Line, cli.Delta) {
	var e struct {
		Type string
		Part struct {
			Text   string
			Tool   string
			Cost   float64
			Tokens struct {
				Input, Output, Reasoning int64
				Cache                    struct{ Read, Write int64 }
			}
			State struct {
				Input  map[string]any
				Output string
				Error  string
			}
		}
		Error struct {
			Type, Name, Message string
			Data                struct{ Message string }
		}
	}
	if json.Unmarshal(raw, &e) != nil {
		return []cli.Line{{Kind: "info", Text: string(raw)}}, cli.Delta{}
	}
	p := e.Part
	switch e.Type {
	case "text":
		return []cli.Line{{Kind: "text", Text: strings.TrimSpace(p.Text)}}, cli.Delta{}
	case "reasoning":
		return []cli.Line{{Kind: "think", Text: strings.TrimSpace(p.Text)}}, cli.Delta{}
	case "tool_use":
		lines := []cli.Line{cli.ToolLine(p.Tool, p.State.Input)}
		if p.State.Error != "" {
			return append(lines, cli.Line{Kind: "err", Text: cli.Clip(p.State.Error, 300)}), cli.Delta{}
		}
		return append(lines, cli.OutLines(p.State.Output)...), cli.Delta{}
	case "step_finish":
		t := p.Tokens
		return nil, cli.Delta{Cost: p.Cost, In: t.Input + t.Cache.Read + t.Cache.Write, Out: t.Output + t.Reasoning}
	case "error":
		// Just the message: the full error repeats it inside an escaped response body.
		er := e.Error
		msg := cmp.Or(er.Message, er.Data.Message, string(raw))
		if kind := cmp.Or(er.Type, er.Name); kind != "" && kind != "unknown" {
			msg = kind + ": " + msg
		}
		return []cli.Line{{Kind: "err", Text: msg}}, cli.Delta{}
	}
	return nil, cli.Delta{}
}
