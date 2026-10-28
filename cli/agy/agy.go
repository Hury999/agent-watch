// Package agy runs agy (`agy -p`) as an agent. It has one login only: no Accounts.
package agy

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/n-delic/agent-watch/cli"
)

type CLI struct{ cli.Base }

func (CLI) Name() string { return "agy" }

// Command starts a conversation agy names itself (init), and continues it by that id after.
func (c CLI) Command(t cli.Turn) (string, []string, []string) {
	// agy can't be kept from editing either (its plan mode runs no commands at all, so a lead couldn't work).
	args := []string{"--output-format", "stream-json", "--dangerously-skip-permissions"}
	if t.Model != "" {
		args = append(args, "--model", t.Model)
	}
	if t.Effort != "" && slices.Contains(c.Efforts(), t.Effort) {
		args = append(args, "--effort", t.Effort)
	}
	prompt := t.Prompt
	if t.Session != "" {
		args = append(args, "--conversation", t.Session)
	} else {
		prompt = cli.WithSystem(t.System, prompt) // agy has no system-prompt flag
	}
	// the prompt follows -p; agy has no flag for images, so their paths stay in the prompt
	return "agy", append(args, "-p", prompt), nil
}

func (CLI) Models(string) ([]cli.Model, error) {
	out, err := exec.Command("agy", "models").Output()
	var list []cli.Model
	for _, line := range strings.Split(string(out), "\n") {
		if slug, name, ok := strings.Cut(line, "\t"); ok { // "<slug>\t<name>"
			list = append(list, cli.Model{ID: slug, Price: name})
		}
	}
	if err == nil && len(list) == 0 {
		err = fmt.Errorf("agy models listed nothing")
	}
	return list, err
}

// NewParser reads agy's stream-json: an init with the conversation id, then step updates. Text arrives in
// pieces, gathered per step until it's DONE; usage comes per step (the result's covers the whole conversation).
func (CLI) NewParser() cli.Parser {
	texts, tools := map[int]string{}, map[int]bool{}
	return func(raw []byte) ([]cli.Line, cli.Delta) {
		var e struct {
			Event          string
			ConversationID string `json:"conversation_id"`
			Message        string
			StepUpdate     struct {
				StepIndex int    `json:"step_index"`
				State     string `json:"state"`
				StepType  string `json:"step_type"`
				TextDelta string `json:"text_delta"`
				ToolName  string `json:"tool_name"`
				ToolInfo  struct {
					Name       string
					Parameters map[string]any
					Output     string
				} `json:"tool_info"`
				Usage struct {
					Input     int64 `json:"input_tokens"`
					Output    int64 `json:"output_tokens"`
					Thinking  int64 `json:"thinking_tokens"`
					CacheRead int64 `json:"cache_read_tokens"`
				}
			} `json:"step_update"`
			Result struct{ Status, Error string }
		}
		if json.Unmarshal(raw, &e) != nil {
			return []cli.Line{{Kind: "info", Text: string(raw)}}, cli.Delta{}
		}
		switch e.Event {
		case "init":
			return nil, cli.Delta{Session: e.ConversationID}
		case "result":
			if e.Result.Status != "SUCCESS" {
				return []cli.Line{{Kind: "err", Text: cmp.Or(e.Result.Error, "agy: "+e.Result.Status)}}, cli.Delta{Limits: quota(e.Result.Error)}
			}
			return nil, cli.Delta{}
		case "error":
			return []cli.Line{{Kind: "err", Text: e.Message}}, cli.Delta{}
		case "step_update":
		default:
			return nil, cli.Delta{}
		}
		s, done := e.StepUpdate, e.StepUpdate.State == "DONE"
		var d cli.Delta
		if done {
			d = cli.Delta{In: s.Usage.Input + s.Usage.CacheRead, Out: s.Usage.Output + s.Usage.Thinking}
		}
		var lines []cli.Line
		switch s.StepType {
		case "agent_response", "planner_response", "thinking":
			texts[s.StepIndex] += s.TextDelta
			if t := strings.TrimSpace(texts[s.StepIndex]); done && t != "" {
				delete(texts, s.StepIndex)
				kind := "text"
				if s.StepType == "thinking" {
					kind = "think"
				}
				lines = append(lines, cli.Line{Kind: kind, Text: t})
			}
		case "tool":
			if !tools[s.StepIndex] {
				tools[s.StepIndex] = true
				lines = append(lines, cli.ToolLine(cmp.Or(s.ToolInfo.Name, s.ToolName), s.ToolInfo.Parameters))
			}
			if done {
				out := strings.ReplaceAll(s.ToolInfo.Output, "\r\n", "\n")
				out, _ = strings.CutPrefix(out, "Command exited with code 0.\n\nCommand output:\n")
				lines = append(lines, cli.OutLines(out)...)
			}
		}
		return lines, d
	}
}

// quota reads "Individual quota reached ... Resets in 14h36m37s": the only usage agy ever reports.
func quota(err string) *cli.Limits {
	m := resetsIn.FindStringSubmatch(err)
	if !strings.Contains(err, "quota") || m == nil {
		return nil
	}
	d, perr := time.ParseDuration(m[1])
	if perr != nil {
		return nil
	}
	return &cli.Limits{Windows: []cli.Window{{Label: "quota", Used: 1, Reset: time.Now().Add(d).Unix()}}}
}

var resetsIn = regexp.MustCompile(`Resets in ((?:\d+[hms])+)`)
