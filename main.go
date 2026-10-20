// agent-watch: a terminal app where a master agent leads slave coding agents and you watch them all live.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(cli(os.Args[1:]))
	}
	if err := runTUI(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
