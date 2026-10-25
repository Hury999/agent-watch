//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// ownGroup puts the command in its own process group so stop can kill the whole tree.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killTree asks the command's whole process group to terminate.
func killTree(cmd *exec.Cmd) { syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
