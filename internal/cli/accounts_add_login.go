package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"

	"github.com/dimipaun/fugaro/internal/claudeplugin"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

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
// The returned error is the child's exit status: a login the owner cancelled
// or that failed is not fatal to the command that called this.
func runClaudeLogin(in io.Reader, out, errOut io.Writer) error {
	for _, name := range claudeLoginCredentialEnv {
		if os.Getenv(name) != "" {
			return userErr("%s is set in this environment, and it decides which account `claude setup-token` logs in to, so the token it prints may be for an account you did not mean to store: unset it and run this again (no login was started)", name)
		}
	}
	if !stdinIsTerminal(in) || !writerIsTerminal(out) {
		return userErr("%s, or pass the token with `%s accounts add <name> --token-stdin` where this is scripted", initflow.NeedsTerminal("--claude"), selfCommand())
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
	return child.Run()
}
