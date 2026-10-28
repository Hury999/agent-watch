package main

// The models the setup's model picker offers, as each CLI lists them (cli.CLI's Models).

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/n-delic/agent-watch/cli"
)

type modelInfo = cli.Model

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

// fetchModels asks an agent's CLI for its account's models, in the background.
func fetchModels(a Agent) tea.Cmd {
	return func() tea.Msg {
		list, err := cliOf(a).Models(expand(a.Data))
		return modelsMsg{accountKey(a), list, err}
	}
}

// listFor is what's known of an agent's models; missing means not fetched yet.
func (f *setupForm) listFor(a Agent) (modelList, bool) {
	l, ok := f.known[accountKey(a)]
	return l, ok
}

// fetch starts loading an account's models unless they're known or on their way.
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
	return cliOf(a).Efforts()
}
