package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Agent is one agent the lead can put to work: a CLI, an account and a model.
type Agent struct {
	Name    string `json:"name"`
	CLI     string `json:"cli"`            // claude or opencode
	Data    string `json:"data,omitempty"` // another account: opencode's database file or Claude's config dir; empty = the default one
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Enabled bool   `json:"enabled"` // the lead may start it
}

// Lead is the agent you talk to. It stands on its own: changing the agents never touches it.
type Lead struct {
	CLI    string `json:"cli"`
	Data   string `json:"data,omitempty"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

type Config struct {
	Lead   Lead    `json:"lead"`
	Agents []Agent `json:"agents"`
}

var clis = []string{"claude", "opencode"}

// defaultConfig has one agent per CLI with its default account and model, enabled if it's installed.
func defaultConfig() Config {
	var c Config
	for _, cli := range clis {
		_, err := exec.LookPath(cli)
		c.Agents = append(c.Agents, Agent{Name: cli, CLI: cli, Enabled: err == nil})
		if err == nil && c.Lead.CLI == "" {
			c.Lead.CLI = cli
		}
	}
	if c.Lead.CLI == "" {
		c.Lead.CLI = "claude"
	}
	return c
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "agent-watch", "config.json")
}

// loadConfig returns the saved config, or the defaults and false when none is saved yet.
func loadConfig() (Config, bool) {
	var c Config
	b, err := os.ReadFile(configPath())
	if err != nil || json.Unmarshal(b, &c) != nil || len(c.Agents) == 0 {
		return defaultConfig(), false
	}
	if c.Lead.CLI == "" { // a v0.1.0 config pointed the lead at an agent instead
		c.Lead.CLI = defaultConfig().Lead.CLI
	}
	return c, true
}

func saveConfig(c Config) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

func (c Config) find(name string) (Agent, bool) {
	for _, a := range c.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// lead is the lead as an agent; its name is its CLI, which is how it's shown.
func (c Config) lead() Agent {
	l := c.Lead
	return Agent{Name: l.CLI, CLI: l.CLI, Data: l.Data, Model: l.Model, Effort: l.Effort}
}

// enabled returns the agent the lead may start under this name.
func (c Config) enabled(name string) (Agent, bool) {
	a, ok := c.find(name)
	return a, ok && a.Enabled
}

// check returns what's wrong with a config before it's saved, or "".
func (c Config) check() string {
	seen := map[string]bool{}
	for _, a := range c.Agents {
		switch {
		case !validName.MatchString(a.Name):
			return "agent names must be lowercase letters, digits and dashes: " + a.Name
		case seen[a.Name]:
			return "two agents are named " + a.Name
		}
		seen[a.Name] = true
	}
	if !slices.Contains(clis, c.Lead.CLI) {
		return "the lead's cli must be claude or opencode"
	}
	return ""
}

// describe is how an agent is shown to you and the lead: "personal opencode-go/x#max".
func describe(a Agent) string {
	model := a.Model
	if model == "" {
		model = "default model"
	}
	switch {
	case a.Effort == "":
		return a.Name + " " + model
	case a.CLI == "claude":
		return a.Name + " " + model + " " + a.Effort
	}
	return a.Name + " " + model + "#" + a.Effort
}

// expand resolves a leading ~ in a data path.
func expand(p string) string {
	if home, err := os.UserHomeDir(); err == nil && (p == "~" || strings.HasPrefix(p, "~/")) {
		return filepath.Join(home, p[1:])
	}
	return p
}

var palette = []lipgloss.Color{cBlue, cPurple, cGreen, cYellow, lipgloss.Color("#F778BA"), lipgloss.Color("#56D4DD")}

// agentColors gives each agent the color of its place in the list, so editing an agent never recolors
// anything. The lead always has the accent color.
var agentColors = map[string]lipgloss.Color{}

func setColors(c Config) {
	clear(agentColors)
	for i, a := range c.Agents {
		agentColors[a.Name] = palette[i%len(palette)]
	}
}

// account identifies the login an agent uses: agents on the same one share its limits and spend.
func accountKey(a Agent) string { return a.CLI + "\x00" + expand(a.Data) }
