package main

// The models the setup's model picker offers: opencode lists an account's own (with prices and effort
// variants); Claude has no list command, so its aliases and current ids are listed here.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type modelInfo struct {
	ID       string
	Price    string   // "free", "$in/$out" per million tokens, or a note
	Variants []string // the model's effort levels
}

// modelList is what's known of one account's models.
type modelList struct {
	list    []modelInfo
	err     error
	loading bool
}

type modelsMsg struct {
	account string // accountKey
	list    []modelInfo
	err     error
}

var claudeEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// shortcut: a fixed list, since claude has no way to list models; update it when new models come out.
var claudeModels = []modelInfo{
	{"sonnet", "latest Sonnet", claudeEfforts}, {"opus", "latest Opus", claudeEfforts},
	{"haiku", "latest Haiku", claudeEfforts}, {"fable", "latest Fable", claudeEfforts},
	{"claude-fable-5-1", "", claudeEfforts}, {"claude-opus-5-5", "", claudeEfforts},
	{"claude-sonnet-5-5", "", claudeEfforts}, {"claude-haiku-5-5", "", claudeEfforts},
}

// fetchModels asks opencode for an account's models, in the background.
func fetchModels(a Agent) tea.Cmd {
	return func() tea.Msg {
		env, args := account(a)
		cmd := exec.Command("opencode", append([]string{"api", "model.list"}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.Output()
		if err != nil {
			return modelsMsg{accountKey(a), nil, fmt.Errorf("opencode api model.list: %w", err)}
		}
		var r struct {
			Data []struct {
				ID, ProviderID string
				Cost           []struct{ Input, Output float64 }
				Variants       []struct{ ID string }
			}
		}
		if err := json.Unmarshal(out, &r); err != nil {
			return modelsMsg{accountKey(a), nil, err}
		}
		var list []modelInfo
		for _, d := range r.Data {
			mi := modelInfo{ID: d.ProviderID + "/" + d.ID, Price: "free"}
			if len(d.Cost) > 0 && (d.Cost[0].Input > 0 || d.Cost[0].Output > 0) {
				mi.Price = fmt.Sprintf("$%g/$%g", d.Cost[0].Input, d.Cost[0].Output)
			}
			for _, v := range d.Variants {
				mi.Variants = append(mi.Variants, v.ID)
			}
			list = append(list, mi)
		}
		slices.SortFunc(list, func(x, y modelInfo) int { return strings.Compare(x.ID, y.ID) })
		return modelsMsg{accountKey(a), list, nil}
	}
}

// listFor is what's known of an agent's models; missing means not fetched yet.
func (f *setupForm) listFor(a Agent) (modelList, bool) {
	if a.CLI == "claude" {
		return modelList{list: claudeModels}, true
	}
	l, ok := f.known[accountKey(a)]
	return l, ok
}

// fetch starts loading an opencode account's models unless they're known or on their way.
func (f *setupForm) fetch(a Agent) tea.Cmd {
	if l, ok := f.listFor(a); ok && l.err == nil {
		return nil // known, or on its way; a failed fetch is tried again
	}
	f.known[accountKey(a)] = modelList{loading: true}
	return fetchModels(a)
}

// matches are the models whose id or price contains every word typed.
func matches(list []modelInfo, typed string) []modelInfo {
	words := strings.Fields(strings.ToLower(typed))
	var out []modelInfo
	for _, mi := range list {
		hay := strings.ToLower(mi.ID + " " + mi.Price)
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(hay, w) }) {
			out = append(out, mi)
		}
	}
	return out
}

// effortChoices are the efforts an agent's model has, if known; "" is the CLI's default.
func (f *setupForm) effortChoices(a Agent) []string {
	l, _ := f.listFor(a)
	for _, mi := range l.list {
		if mi.ID == a.Model && len(mi.Variants) > 0 {
			return append([]string{""}, mi.Variants...)
		}
	}
	return efforts
}
