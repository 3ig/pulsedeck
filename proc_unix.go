//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach lets a relaunched server outlive the process that started it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func attachConsole() {}

// quiet is a no-op here; see proc_windows.go.
func quiet(cmd *exec.Cmd) *exec.Cmd { return cmd }
