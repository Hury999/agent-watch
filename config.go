package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Agent is one configured agent CLI with its model and effort.
type Agent struct {
	Agent   string `json:"agent"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
	Enabled bool   `json:"enabled"`
}

type Config struct {
	Master Agent   `json:"master"`
	Slaves []Agent `json:"slaves"`
}

var kinds = []string{"claude", "opencode-decomp", "opencode-personal"}

// defaults returns the model and effort a fresh agent of this kind starts with.
func defaults(kind string) Agent {
	if kind == "claude" {
		return Agent{Agent: kind, Model: "sonnet", Effort: "high"}
	}
	return Agent{Agent: kind, Model: "opencode-go/deepseek-v4.1-flash", Effort: "max"}
}

func defaultConfig() Config {
	c := Config{Master: defaults("claude")}
	for _, k := range []string{"opencode-decomp", "opencode-personal", "claude"} {
		s := defaults(k)
		s.Enabled = k != "claude"
		c.Slaves = append(c.Slaves, s)
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
	if err != nil || json.Unmarshal(b, &c) != nil {
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

// slave returns the enabled slave of this kind.
func (c Config) slave(kind string) (Agent, bool) {
	for _, s := range c.Slaves {
		if s.Agent == kind && s.Enabled {
			return s, true
		}
	}
	return Agent{}, false
}

// describe is how an agent is shown to the user and the master: "opencode-decomp opencode-go/x#max".
func describe(a Agent) string {
	switch {
	case a.Effort == "":
		return a.Agent + " " + a.Model
	case a.Agent == "claude":
		return a.Agent + " " + a.Model + " " + a.Effort
	}
	return a.Agent + " " + a.Model + "#" + a.Effort
}
