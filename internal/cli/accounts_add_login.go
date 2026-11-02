package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/claudeplugin"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// This file is §3's two acts and the handover between them: the login on the
// owner's own terminal (act one) and the hidden paste that follows it (act two),
// which is the read the command performs with or without --claude. Nothing the
// login prints is read, kept or reprinted — the paste is the only token path
// (D862's answer (a), spec §3.2).

// claudeLoginCredentialEnv are the two variables that decide which account
// Anthropic's own login lands on: with either of them set the command stops
// before any child starts, because a token minted for the wrong account is a
// token the owner did not mean to store. It is a refusal and never a
// deletion: fugaro does not edit the owner's environment, it declines to
// guess (spec §3.1 step 3, §6(b)).
var claudeLoginCredentialEnv = []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}

// runClaudeLogin runs Anthropic's own login, `claude setup-token`, on the
// owner's own terminal: the child inherits in, out and errOut, runs in
// fugaro's own process group, and fugaro holds its own SIGINT for the
// duration, so the owner's keystrokes and the terminal's signals reach the
// login directly and the login's own handling of a cancel decides. Nothing of
// what it prints is read, kept or reprinted (spec §3.1 steps 1, 4 and 5).
//
// A login that ran and exited non-zero comes back as *claudeLoginStatus: the
// status is the child's own and not a failure of the command that called this
// (§3.3). A child that could not start at all is a refusal, because there is
// no status to report and nothing the owner could have answered.
func runClaudeLogin(in io.Reader, out, errOut io.Writer) error {
	for _, name := range claudeLoginCredentialEnv {
		if os.Getenv(name) != "" {
			return userErr("%s is set in this environment, and it decides which account `claude setup-token` logs in to, so the token it prints may be for an account you did not mean to store: unset it and run this again (no login was started)", name)
		}
	}
	if !stdinIsTerminal(in) || !writerIsTerminal(out) {
		// The alternative is the command without this flag, which is a path the
		// command already has: without a terminal the value is read from stdin
		// (§2.2 step 3). It is not a flag of its own — `accounts add` carries no
		// flag that takes a value (spec §2.1) — so the sentence names the other
		// command rather than a `--token-stdin` that does not exist.
		return userErr("%s; to script this, run it without --claude and pipe the token on stdin", initflow.NeedsTerminal("--claude"))
	}
	bin, err := claudeplugin.Find(claudeLookPath)
	switch {
	case errors.Is(err, claudeplugin.ErrNoClaude):
		return userErr("claude is not on PATH: --claude runs Anthropic's own `claude setup-token` on your terminal, so mint the token another way and paste it at the prompt")
	case err != nil:
		return userErr("%v", err)
	}
	fmt.Fprintf(out, "  running: %s setup-token\n", pluginwire.Printable(bin))

	// Holding the signal is the whole point and nothing reads the channel:
	// while it is registered fugaro does not die on the owner's Ctrl-C, and
	// the terminal delivers that Ctrl-C to the login itself, which is the
	// process group's foreground job. There is no terminal state to restore
	// afterwards, because fugaro changed none.
	held := make(chan os.Signal, 1)
	signal.Notify(held, os.Interrupt)
	defer signal.Stop(held)

	// Not exec.CommandContext: a cancelled context would kill the login, and
	// the login's own handling of a cancel is what decides.
	child := exec.Command(bin, "setup-token")
	child.Stdin, child.Stdout, child.Stderr = in, out, errOut
	// The owner's own environment, unchanged: fugaro adds nothing to it (the
	// token does not exist yet) and removes nothing from it.
	child.Env = os.Environ()
	// No Setpgid: the child stays in fugaro's own process group, so it is the
	// terminal's foreground job and nothing sits between the owner and it.
	err = child.Run()
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return &claudeLoginStatus{err: err}
	}
	if err != nil {
		return userErr("running %s setup-token: %v", pluginwire.Printable(bin), err)
	}
	return nil
}

// claudeLoginStatus is the login's own exit status: the child ran on the
// owner's terminal and ended non-zero, which is not a failure of the command
// that called it. A login the owner cancelled or that failed stores nothing and
// still leaves the paste, so the command costs one paste rather than itself
// (spec §3.3).
type claudeLoginStatus struct{ err error }

func (e *claudeLoginStatus) Error() string { return e.err.Error() }
func (e *claudeLoginStatus) Unwrap() error { return e.err }

// accountsAddClaudeLogin is --claude's act one and the handover to act two
// (spec §3): it runs the login on the owner's own terminal, reports one that
// did not finish without failing the command, and leaves the token itself to
// the hidden prompt below, which is the only path a value ever takes.
//
// It refuses in a coding agent's session before anything runs (D868, the
// owner's ruling of 2026-11-01). The fence is here rather than only on the
// write path because it is the LOGIN that must not happen in a session: it
// prints a credential on a terminal that a session's transcript may hold, and
// the command around it stores that credential project-wide. A session writes
// nothing here, and --yes does not lift it.
func accountsAddClaudeLogin(cmd *cobra.Command, path, what, command string) error {
	if marker := agentMarker(os.Getenv); marker != "" {
		return userErr("refusing to run Anthropic's login for %s in a coding agent's session (%s is set): %s; run this yourself at your own terminal: %s; nothing was written",
			path, marker, what, command)
	}
	err := runClaudeLogin(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	var status *claudeLoginStatus
	if errors.As(err, &status) {
		// The status only: what the login printed is the login's and is never
		// quoted, tailed or rephrased (§3.3).
		fmt.Fprintf(cmd.ErrOrStderr(), "the login exited with %v: nothing is stored from the login, and the hidden prompt below is the only token path — paste the token it printed above, or press Ctrl-C and run again without --claude\n", status)
		return nil
	}
	return err
}
