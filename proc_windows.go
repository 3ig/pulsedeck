package main

import (
	"os"
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200} // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
}

// quiet stops a helper program from opening a console window. The server is a
// GUI program with no console of its own, so without this every command-line
// tool it runs (nvidia-smi every few seconds, plugins, PowerShell for media
// keys) would flash a window and steal keyboard focus.
func quiet(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}

// attachConsole reconnects stdout to the terminal the program was started
// from. The Windows build is a GUI program (so double-clicking it opens no
// console window), which otherwise makes flags like -sensors print nothing.
func attachConsole() {
	// Output already goes somewhere (a pipe, a file, an SSH session): keep it.
	if h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != syscall.InvalidHandle {
		if t, _ := syscall.GetFileType(h); t != 0 {
			return
		}
	}
	const attachParentProcess = ^uintptr(0)
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := proc.Call(attachParentProcess); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}
