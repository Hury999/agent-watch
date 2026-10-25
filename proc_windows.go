package main

import (
	"os/exec"
	"strconv"
	"syscall"
)

// ownGroup gives the command its own process group (CREATE_NEW_PROCESS_GROUP).
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x200} }

// killTree kills the command and all its descendants.
func killTree(cmd *exec.Cmd) {
	exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
