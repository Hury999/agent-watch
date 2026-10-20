package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

// Lead is the agent you talk to: one of the agents, with its own model and effort.
type Lead struct {
	Agent  string `json:"agent"`
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
		if err == nil && c.Lead.Agent == "" {
			c.Lead.Agent = cli
		}
	}
	if c.Lead.Agent == "" {
		c.Lead.Agent = "claude"
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

// lead is the lead's agent with the lead's model and effort.
func (c Config) lead() Agent {
	a, ok := c.find(c.Lead.Agent)
	if !ok && len(c.Agents) > 0 {
		a = c.Agents[0]
	}
	a.Model, a.Effort = c.Lead.Model, c.Lead.Effort
	return a
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
	if !seen[c.Lead.Agent] {
		return "the lead's agent " + c.Lead.Agent + " doesn't exist"
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

// agentColors gives each configured agent its own color: the first Claude agent gets Claude's clay.
var agentColors = map[string]lipgloss.Color{}

func setColors(c Config) {
	clear(agentColors)
	clay, next := false, 0
	for _, a := range c.Agents {
		if a.CLI == "claude" && !clay {
			agentColors[a.Name], clay = cAccent, true
			continue
		}
		agentColors[a.Name] = palette[next%len(palette)]
		next++
	}
}
