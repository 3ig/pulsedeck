package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Action is something a client may trigger by id. The platform specifics live
// in actions_<os>.go (mediaCmd, openCmd, lockCmd).
type Action struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Icon    string   `json:"icon,omitempty"`
	Type    string   `json:"type"`              // media | open | lock | command
	Arg     string   `json:"arg,omitempty"`     // media key, or URL/path for "open"
	Command []string `json:"command,omitempty"` // argv for "command"
}

func runAction(a Action) error {
	switch a.Type {
	case "media":
		return runWait(mediaCmd(a.Arg))
	case "lock":
		return runWait(lockCmd())
	case "open":
		if a.Arg == "" {
			return errors.New("action has no target")
		}
		return runWait(openCmd(a.Arg))
	case "command":
		if len(a.Command) == 0 {
			return errors.New("action has no command")
		}
		// Launch and detach: commands may be long-running applications.
		cmd := quiet(exec.Command(a.Command[0], a.Command[1:]...))
		cmd.Env = append(os.Environ(), sessionEnv()...)
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	}
	return fmt.Errorf("unknown action type %q", a.Type)
}

func runWait(argv []string) error {
	if len(argv) == 0 {
		return errors.New("not supported on this platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := quiet(exec.CommandContext(ctx, argv[0], argv[1:]...))
	cmd.Env = append(os.Environ(), sessionEnv()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if msg != "" {
			return fmt.Errorf("%v: %s", err, msg)
		}
		return err
	}
	return nil
}
