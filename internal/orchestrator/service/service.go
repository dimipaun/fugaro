// Package service is the login unit that keeps the orchestrator daemon
// running: what it says, where it lives, and how the platform's own service
// manager is told to read it (O9, D1232).
//
// A unit is one file under the person's own home, and it runs one program:
// `fugaro orchestrator run`, with the stable binary path named twice — once as
// the program to run and once as the `--binary` argument (see
// dev.fugaro.orchestrator's template). It is a login unit and not a system
// service: nothing here touches a system path, needs root, or asks for sudo,
// and the manager it reloads is the person's own, at their own login level.
//
// The unit restarts the daemon after a crash or a reboot (launchd's KeepAlive,
// systemd's Restart=always), and never treats a clean exit as done: the daemon
// exits 0 on purpose when the binary the unit names has moved on, so that the
// unit starts the new one (orchestrator_version.go, O8). A unit that stopped
// there would leave the daemon it was handed from dead.
//
// Nothing from the project, its state or its policy goes into the unit. That is
// what makes the re-render `fugaro update` does a comparison of exactly two
// things — the template and the binary path — and why nothing has to be
// stamped, migrated or versioned here: the daemon reads everything else itself,
// and the state it migrates is its own start-up's business.
package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runManager runs one service manager command and returns its combined output.
// It is the whole of how this package reaches launchctl and systemctl: no
// shell, no sudo, no other tool, and no environment of its own.
func runManager(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
	}
	return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
}

// Label is the unit's name on both platforms: the launchd label, and the
// basename of the systemd user unit (fugaro-orchestrator.service). It is a
// reverse-DNS name like every launchd label, under the same domain the rest of
// this project's identifiers use.
const Label = "dev.fugaro.orchestrator"

// systemdName is the systemd user unit's file name. systemd's own name and not
// the launchd label: a unit name is not a label there, and the two are read by
// two different managers on two different machines.
const systemdName = "fugaro-orchestrator.service"

// Unit is one platform's login unit: what it is called, where its file belongs
// and its whole text.
type Unit struct {
	// Platform is the service manager's own name, "launchd" or "systemd". It
	// is what the plan lines and the messages print, so a person reading them
	// knows which manager the line is about.
	Platform string
	// Name is the unit's name to that manager: launchd's label, systemd's unit
	// name.
	Name string
	// Path is the file the unit lives in, under the person's home.
	Path string
	// Text is the file's whole content, rendered from this release's fixed
	// template.
	Text string
}

// Manager is one platform's login service manager. The two implementations
// here run launchctl and systemctl; every test fakes the interface rather than
// the machine, because nothing in a test may reach a real service manager.
type Manager interface {
	// Platform is the manager's own name.
	Platform() string
	// Render is this platform's unit for the binary at binaryPath, and the
	// path its file belongs at under this person's home (found through getenv,
	// so the Windows-port rule for a home directory is one function,
	// localcfg's).
	Render(getenv func(string) string, binaryPath string) (Unit, error)
	// Reload makes the manager read the unit's file: it loads a unit that is
	// not loaded and restarts the one that is. It is what `fugaro orchestrator
	// install` and the converge's re-render both call, so a re-rendered file is
	// never left on disk unread.
	Reload(ctx context.Context, u Unit) error
	// Remove unloads the unit: the manager stops reading the file. The file
	// itself is the caller's to delete — it is the person's own file under
	// their home, and this manager never wrote it. A unit that was never
	// loaded is not an error: there is nothing to remove, which is exactly
	// what removing it means.
	Remove(ctx context.Context, u Unit) error
}

// UnsupportedError is For's refusal on a platform that carries no login unit
// for this CLI. D1232 is macOS and Linux only: Windows has neither manager, and
// Fugaro on Windows is the CLI inside WSL (docs/windows-cli), where the unit is
// the Linux one. It carries the platform so the refusal names it.
type UnsupportedError struct {
	// GOOS is the platform the unit was asked for.
	GOOS string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("the orchestrator's login unit is not supported on %s: fugaro installs one on macOS (launchd) and Linux (systemd) only, and runs the daemon in the foreground everywhere else (fugaro orchestrator run)", e.GOOS)
}

// For is the login service manager of goos. goos is a parameter rather than a
// read of runtime.GOOS so both platforms are testable from one checkout, the
// way localcfg.UserDir is.
func For(goos string) (Manager, error) {
	switch goos {
	case "darwin":
		return launchd{}, nil
	case "linux":
		return systemd{}, nil
	}
	return nil, &UnsupportedError{GOOS: goos}
}

// Render is the one renderer: the unit this release installs for the binary at
// binaryPath, on the platform named by goos. `fugaro orchestrator install`, the
// converge's setup step and the converge's update step all come through it, so
// there is no second template anywhere to drift from this one — the update
// step's comparison is between this text and the file on disk and nothing else.
func Render(getenv func(string) string, goos, binaryPath string) (Unit, error) {
	m, err := For(goos)
	if err != nil {
		return Unit{}, err
	}
	return m.Render(getenv, binaryPath)
}
