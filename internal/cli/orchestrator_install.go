package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/orchestrator/service"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro orchestrator install|uninstall|status: the login unit that runs the
// daemon (O9, D1232).
//
// The three are the person's own commands and nothing else's. They read no
// project, reach no network and open no database: a unit names one program and
// one argument and carries nothing from a project's state or policy, and the
// daemon reads all of that itself. That is also why uninstall works on a
// machine whose local config is gone — which is the machine most likely to need
// it — and why the unit's path is the same one the converge's steps use
// (service.Render is the only renderer, and `~`/XDG decide where it goes).
//
// Nothing here touches a system path, needs root or asks for sudo: the unit
// lives under the person's own home and the service manager is asked at the
// person's own login level (launchd's gui/<uid>, systemd's --user).

// orchestratorService is the login service manager this machine has. Tests
// replace it, so no test ever runs launchctl or systemctl.
var orchestratorService = func() (service.Manager, error) { return service.For(runtime.GOOS) }

// orchestratorGOOS is the platform the unit is rendered for: runtime.GOOS, as
// a variable so that the one refusal a checkout of another platform cannot
// otherwise reach — Windows — is testable from anywhere (service.For is a
// parameter for the same reason, and this is the CLI's half of it).
var orchestratorGOOS = runtime.GOOS

// orchestratorReload loads or reloads an installed unit. It is the one reload
// every writer uses — `fugaro orchestrator install`, the converge's setup step
// and the converge's update step — so a re-rendered file is never left unread
// by the manager, and tests count its calls instead of reaching a service
// manager.
var orchestratorReload = func(ctx context.Context, m service.Manager, u service.Unit) error {
	return m.Reload(ctx, u)
}

// orchestratorUnit is the unit this release would install on this machine: the
// platform's template, rendered for the stable path of the binary this process
// is running.
func orchestratorUnit() (service.Unit, error) {
	exe, err := os.Executable()
	if err != nil {
		return service.Unit{}, fmt.Errorf("finding the binary this %s is: %w", selfCommand(), err)
	}
	u, err := service.Render(os.Getenv, orchestratorGOOS, stableBinaryPath(exe))
	if err != nil {
		return service.Unit{}, err
	}
	return u, nil
}

// stableBinaryPath is the path a login unit may name: the package manager's
// stable link rather than the versioned path this process resolved.
//
// Homebrew is the one layout this knows, and it is the one that matters: brew
// upgrade replaces <prefix>/Cellar/fugaro/<version>/bin/fugaro and keeps
// <prefix>/opt/fugaro, so a unit naming the Cellar path would name a file that
// is gone after the first upgrade — the daemon's own version check (O8) would
// fail forever and the unit's KeepAlive would restart a program that no longer
// exists. The prefixes are the three Homebrew installs this CLI ships through
// and not the segment name "Cellar": a directory that merely carries that name
// is not a package manager's, and rewriting it would name an opt link nobody
// wrote — the same rule the last paragraph applies to every other path, which
// is used as it is. A Homebrew at some other prefix is that case too: the path
// is kept, because nothing here can tell it from an ordinary directory.
func stableBinaryPath(exe string) string {
	path := filepath.ToSlash(exe)
	for _, prefix := range []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"} {
		rest, ok := strings.CutPrefix(path, prefix+"/Cellar/")
		if !ok {
			continue
		}
		// <name>/<version>/<the rest>: without a version segment there is
		// nothing to strip, and the link would be invented rather than found.
		parts := strings.Split(rest, "/")
		if len(parts) < 3 {
			return exe
		}
		return prefix + "/opt/" + parts[0] + "/" + strings.Join(parts[2:], "/")
	}
	return exe
}

// unitTextOnDisk is the text of the unit's file and whether there is one. A
// file that cannot be read is an error and never "no unit": the update step
// must not read an unreadable unit as an absent one and write over it.
func unitTextOnDisk(u service.Unit) (string, bool, error) {
	data, err := os.ReadFile(u.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return string(data), true, nil
}

// writeUnit writes the unit's file, and the directory it belongs in, under the
// person's own home. It is the one writer: install, the converge's setup step
// and the converge's update step all write through it.
//
// The file is 0644 like every other unit in those directories, and it carries
// no secret to be more careful with: nothing from a project, its state or its
// policy goes into a unit, which is a property of the template and not of this
// write.
func writeUnit(u service.Unit) error {
	if err := os.MkdirAll(filepath.Dir(u.Path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(u.Path), err)
	}
	if err := os.WriteFile(u.Path, []byte(u.Text), 0o644); err != nil {
		return fmt.Errorf("writing the login unit at %s: %w", u.Path, err)
	}
	return nil
}

// installUnit is the one installer `fugaro orchestrator install` and the two
// converge steps share: it writes the unit's file and reloads the login service
// so the manager reads it. It confirms nothing and prints nothing — the
// confirmation belongs to the caller, and the plan line the caller showed is
// what it is over — and it never migrates state: a state node left at an older
// schema version is migrated by the daemon's own start-up (O8, T7).
//
// It is idempotent in the sense the command documents: writing the file again
// is a re-render from the current template, and the reload is what replaces the
// daemon that is running with the one that file names.
func installUnit(ctx context.Context, m service.Manager, u service.Unit) error {
	if err := writeUnit(u); err != nil {
		return err
	}
	if err := orchestratorReload(ctx, m, u); err != nil {
		// The file is written and the manager did not read it, so this is the
		// one state the two converge steps cannot repair: the next run compares
		// the text, finds it current, and reloads nothing (a reload with nothing
		// to reload would make every run noisy). The failure names the command
		// that loads the file regardless of its text, which is the way out.
		return fmt.Errorf("%w (the unit's file is written at %s; `%s orchestrator install` loads it again)",
			err, safetext.Field(u.Path), selfCommand())
	}
	return nil
}

// removeUnit takes the unit out of the person's login: the manager is told to
// stop reading it, and the file is deleted. The manager is asked first, while
// the file it names is still there, and a manager that reports the unload as
// failed stops the deletion here: a unit the manager may still be reading is
// not one to delete out from under it, and the person is told so. Both
// managers this CLI ships treat a failing unload as "there was nothing to
// unload" (the service package's own reading of a unit that was never loaded),
// so this is the branch a manager that does report one gets.
func removeUnit(ctx context.Context, m service.Manager, u service.Unit) error {
	if err := m.Remove(ctx, u); err != nil {
		return fmt.Errorf("unloading the login unit %s: %w", u.Name, err)
	}
	if err := os.Remove(u.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", u.Path, err)
	}
	return nil
}

// unitActWords is what an install does, in the words the confirmation is over:
// the file, and the fact that what it runs runs again at every login.
const unitActWords = "writes the login unit that runs the orchestrator daemon: it starts the daemon at every login, restarts it when it stops, and the daemon drives this project's orchestrator until it is uninstalled"

func newOrchestratorInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install or re-render the login unit that runs the daemon",
		Long: "Write the per-user login unit that runs `fugaro orchestrator run` and load\n" +
			"it, so the daemon starts at every login and is restarted when it stops.\n" +
			"Nothing else is written: no system path is touched, no root is needed, and\n" +
			"the service manager is asked at your own login level (launchd on macOS,\n" +
			"systemd --user on Linux; Windows has neither, and answers so).\n\n" +
			"The unit names the stable path of this fugaro — Homebrew's opt link and\n" +
			"never a versioned Cellar path that an upgrade removes — twice: as the\n" +
			"program to run, and as the `--binary` argument whose version the daemon\n" +
			"compares with its own before it hands over to a new release.\n\n" +
			"It is idempotent: a unit that is already there is re-rendered from this\n" +
			"release's template and the service restarted, which is also what `fugaro\n" +
			"update` does to an installed unit (and `fugaro update` never installs\n" +
			"one). It writes no state and migrates none: the daemon's own start-up\n" +
			"does that.\n\n" +
			"A unit is code that runs at every login, so this command asks you to type\n" +
			"the word install at a real terminal, has no flag that stands in for it,\n" +
			"and refuses in a coding agent's session. Running it yourself, without the\n" +
			"marker, is the way to do it: whoever can do that can write the file by\n" +
			"hand anyway.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorInstall(cmd) },
	}
	return cmd
}

func runOrchestratorInstall(cmd *cobra.Command) error {
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("this is a coding agent's session (%s is set), and a login unit is code that runs at every login: nothing is installed here; run `%s orchestrator install` in your own terminal window", safetext.Field(m), selfCommand())
	}
	u, err := orchestratorUnit()
	if err != nil {
		return userErr("%v", err)
	}
	m, err := orchestratorService()
	if err != nil {
		return userErr("%v", err)
	}
	out := cmd.OutOrStdout()
	was, ok, err := unitTextOnDisk(u)
	if err != nil {
		return userErr("reading %s: %v", u.Path, err)
	}
	switch {
	case !ok:
		fmt.Fprintf(out, "writes the login unit %s\n", safetext.Field(u.Path))
	case was == u.Text:
		fmt.Fprintf(out, "re-renders the login unit %s (it is already this release's template) and restarts it\n", safetext.Field(u.Path))
	default:
		fmt.Fprintf(out, "re-renders the login unit %s: the template or the binary path changed\n", safetext.Field(u.Path))
	}
	if err := confirmUnitInstall(cmd, u); err != nil {
		return err
	}
	if err := installUnit(cmd.Context(), m, u); err != nil {
		return err
	}
	fmt.Fprintf(out, "Installed %s and loaded it with %s. Uninstall it with `%s orchestrator uninstall`.\n", safetext.Field(u.Path), u.Platform, selfCommand())
	return nil
}

// confirmUnitInstall is this command's own confirmation: the word install typed
// at a real terminal, and nothing else. It is the strictest class there is for
// a local write — a unit runs code at every login — and this command has no
// --yes for it to be given by, which is what "no flag stands in for it" means.
// The refusal without a terminal names the command to run somewhere a person
// is, rather than a flag that does not exist.
func confirmUnitInstall(cmd *cobra.Command, u service.Unit) error {
	out := cmd.OutOrStdout()
	if !stdinIsTerminal(cmd.InOrStdin()) {
		return userErr("%s: `%s orchestrator install` %s", initflow.NeedsTerminal("the typed confirmation of this step"), selfCommand(), unitActWords)
	}
	fmt.Fprintf(out, "⚠ CONFIRM: %s\n", unitActWords)
	fmt.Fprintf(out, "Type install to confirm: ")
	// restore.go's readConfirmLine, which stops at the newline and returns an
	// input that ended as the line it read: an EOF is a word that is not the
	// one asked for, which the comparison below refuses.
	line, err := readConfirmLine(cmd.InOrStdin())
	if err != nil && !errors.Is(err, io.EOF) {
		return userErr("reading the confirmation: %v", err)
	}
	if strings.TrimSpace(line) != "install" {
		return userErr("not confirmed (the word install was not typed); nothing was written")
	}
	noteIDETerminalCmd(cmd, out)
	return nil
}

func newOrchestratorUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the login unit that runs the daemon",
		Long: "Unload the login unit and delete its file, so the daemon stops being\n" +
			"started at login and stops being restarted. It asks nothing: removing the\n" +
			"unit takes away code that runs at every login rather than adding it, and\n" +
			"`fugaro orchestrator install` writes it back after its own typed\n" +
			"confirmation.\n\n" +
			"Nothing else is touched: the project's orchestrator state, its lease and\n" +
			"its audit log are in the database and not in the unit, `fugaro orchestrator\n" +
			"run` still runs in the foreground without a unit, and a machine with no\n" +
			"unit installed is not an error — it says so and returns.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorUninstall(cmd) },
	}
	return cmd
}

func runOrchestratorUninstall(cmd *cobra.Command) error {
	u, err := orchestratorUnit()
	if err != nil {
		return userErr("%v", err)
	}
	m, err := orchestratorService()
	if err != nil {
		return userErr("%v", err)
	}
	out := cmd.OutOrStdout()
	if _, ok, err := unitTextOnDisk(u); err != nil {
		return userErr("reading %s: %v", u.Path, err)
	} else if !ok {
		fmt.Fprintf(out, "no login unit is installed at %s: nothing to remove\n", safetext.Field(u.Path))
		return nil
	}
	if err := removeUnit(cmd.Context(), m, u); err != nil {
		return err
	}
	fmt.Fprintf(out, "Removed %s and unloaded it with %s. The daemon is not started at login any more; `%s orchestrator install` writes the unit back.\n", safetext.Field(u.Path), u.Platform, selfCommand())
	return nil
}

func newOrchestratorStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report the login unit that runs the daemon",
		Long: "Report whether this machine has the login unit that runs the orchestrator\n" +
			"daemon, where its file is, and whether it is what this release's template\n" +
			"would write. It changes nothing and reads no project: the file is the\n" +
			"whole of what it looks at, and `fugaro doctor` reports the same drift as\n" +
			"an update being due.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorStatus(cmd) },
	}
	return cmd
}

func runOrchestratorStatus(cmd *cobra.Command) error {
	u, err := orchestratorUnit()
	if err != nil {
		return userErr("%v", err)
	}
	out := cmd.OutOrStdout()
	was, ok, err := unitTextOnDisk(u)
	switch {
	case err != nil:
		return userErr("reading %s: %v", u.Path, err)
	case !ok:
		fmt.Fprintf(out, "the login unit is not installed: %s does not exist\n", safetext.Field(u.Path))
		fmt.Fprintf(out, "  `%s orchestrator install` writes it (fugaro setup does it as one of its steps)\n", selfCommand())
		return nil
	case was == u.Text:
		fmt.Fprintf(out, "the login unit at %s is installed and current (%s)\n", safetext.Field(u.Path), u.Platform)
		return nil
	}
	fmt.Fprintf(out, "the login unit at %s is out of date: it was rendered from an older template or another binary path (%s)\n", safetext.Field(u.Path), u.Platform)
	fmt.Fprintf(out, "  `%s update` re-renders it, or `%s orchestrator install` does it on its own\n", selfCommand(), selfCommand())
	return nil
}
