// agent-watch: a terminal app where a lead agent puts coding agents to work and you watch them all live.
package main

import (
	"fmt"
	"os"
)

var version = "dev" // set by release builds

const help = `agent-watch (aw): a lead agent puts coding agents to work, and you watch them all live.

usage: aw                open a new run in this directory
       aw -c             continue the latest run in this directory
       aw -r             pick a run of this directory to resume
       aw -v             version
       aw -h             this help

The lead runs these itself:
       aw spawn <name> <type> <workdir> < brief
       aw send <name> < message
       aw stop <name>`

func main() {
	resume := ""
	if len(os.Args) > 1 {
		cwd, _ := os.Getwd()
		switch os.Args[1] {
		case "-v", "--version", "version":
			fmt.Println("agent-watch", version)
			return
		case "-h", "--help", "help":
			fmt.Println(help)
			return
		case "-c", "--continue":
			runs := runsIn(cwd)
			if len(runs) == 0 {
				fmt.Fprintln(os.Stderr, "agent-watch: no saved runs in", cwd)
				os.Exit(1)
			}
			resume = runs[0]
		case "-r", "--resume":
			run, err := pickRun(cwd)
			if err != nil {
				fmt.Fprintln(os.Stderr, "agent-watch:", err)
				os.Exit(1)
			}
			resume = run
		default:
			os.Exit(leadCLI(os.Args[1:]))
		}
	}
	if err := runTUI(resume); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
