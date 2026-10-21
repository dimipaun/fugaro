package cli

// The root `-v, --version` flag: the conventional spelling of the `version`
// subcommand. The owner typed `fugaro -v` and `fugaro --version` and got
// "unknown shorthand flag: 'v'" / "unknown flag: --version".
//
// The flag is cobra's own (the root command's Version field plus a version
// template), which answers it inside execute, before a subcommand is resolved
// and before the root's PersistentPreRun -- so it prints the version, exits 0
// and runs nothing: no checkout read, no local config, no cloud call, no
// update check. The tests below pin each half of that, and the byte-for-byte
// sameness with `fugaro version` that makes the two spellings one fact.

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// executeResolved runs args through a fresh tree like execute, and also
// returns the command cobra resolved and executed. `fugaro --version cancel`
// must execute the ROOT command -- the flag answers before a subcommand is
// reached -- and only ExecuteContextC's first result can show that.
func executeResolved(t *testing.T, args ...string) (*cobra.Command, string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(NormalizeRepoArgs(args))
	executed, err := cmd.ExecuteContextC(context.Background())
	return executed, out.String(), errOut.String(), err
}

// TestVersionFlagPrintsTheSubcommandLine: --version and -v print exactly what
// `fugaro version` prints, byte for byte, on stdout, with nothing on stderr.
// Want is the subcommand's own output rather than a literal, because "the same
// bytes" is the property: one version, one line, one source of truth.
func TestVersionFlagPrintsTheSubcommandLine(t *testing.T) {
	want, _, err := execute(t, "version")
	if err != nil {
		t.Fatalf("fugaro version: %v", err)
	}
	if want != "dev\n" {
		// Not fatal: the byte-for-byte comparison below is the point of this
		// test, and it has to run even when the subcommand's own line moved.
		t.Errorf("fugaro version printed %q, want the development build's %q", want, "dev\n")
	}
	for _, arg := range []string{"--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			out, errOut, err := execute(t, arg)
			if err != nil {
				t.Fatalf("fugaro %s: %v", arg, err)
			}
			if out != want {
				t.Errorf("fugaro %s printed %q, fugaro version prints %q", arg, out, want)
			}
			if errOut != "" {
				t.Errorf("fugaro %s wrote %q to stderr, want nothing", arg, errOut)
			}
		})
	}
}

// TestVersionFlagBeforeASubcommandDoesNotRunIt: the flag is answered at the
// root, before cobra resolves or runs anything below it, so `fugaro --version
// cancel` prints the version rather than cancelling. cancel is the argument on
// purpose: it takes exactly one run ID, so if the subcommand were reached this
// test would see its usage error instead of the version line.
func TestVersionFlagBeforeASubcommandDoesNotRunIt(t *testing.T) {
	if _, _, err := execute(t, "cancel"); err == nil {
		t.Fatal("fugaro cancel with no run ID succeeded: this test's argument no longer proves the subcommand was skipped")
	}
	for _, arg := range []string{"--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			executed, out, errOut, err := executeResolved(t, arg, "cancel")
			if err != nil {
				t.Fatalf("fugaro %s cancel: %v", arg, err)
			}
			if out != "dev\n" {
				t.Errorf("fugaro %s cancel printed %q, want %q", arg, out, "dev\n")
			}
			if errOut != "" {
				t.Errorf("fugaro %s cancel wrote %q to stderr, want nothing", arg, errOut)
			}
			if got := executed.CommandPath(); got != "fugaro" {
				t.Errorf("fugaro %s cancel executed %q, want the root command: the flag must answer before a subcommand is reached", arg, got)
			}
		})
	}
}

// TestVersionFlagAnswersBeforeTheRootPreRun: cobra answers its version flag
// inside execute, before PersistentPreRun, which is what makes the flag usable
// with no config and no cloud: the hook installs the update-check switch in
// the command's context, and nothing installs it when the version flag answers
// the command. The other direction is checked too (a real command does run the
// hook), so this test cannot pass by the value never being installed at all.
func TestVersionFlagAnswersBeforeTheRootPreRun(t *testing.T) {
	for _, arg := range []string{"--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			executed, _, _, err := executeResolved(t, arg)
			if err != nil {
				t.Fatalf("fugaro %s: %v", arg, err)
			}
			if _, ran := updateCheckDisabledFromContext(executed.Context()); ran {
				t.Errorf("fugaro %s ran the root PersistentPreRun, which reads the checkout and the local config and kicks off the update check: the version flag must answer before it", arg)
			}
		})
	}
	executed, _, _, err := executeResolved(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	if _, ran := updateCheckDisabledFromContext(executed.Context()); !ran {
		t.Error("fugaro version did not run the root PersistentPreRun, so this test can no longer tell whether the flag short-circuits it")
	}
}

// TestVersionFlagNeedsNoCheckoutOrProject: the flag answers from the binary
// alone. firstRunEnv makes the working directory a temp directory that is not
// a checkout, with HOME, XDG_CONFIG_HOME and the project environment pointed
// at nothing, so a version answer that depended on any of them would refuse or
// print something else here.
func TestVersionFlagNeedsNoCheckoutOrProject(t *testing.T) {
	firstRunEnv(t)
	for _, arg := range []string{"--version", "-v", "version"} {
		out, errOut, err := execute(t, arg)
		if err != nil {
			t.Fatalf("fugaro %s outside a checkout and with no project: %v", arg, err)
		}
		if out != "dev\n" {
			t.Errorf("fugaro %s printed %q, want %q", arg, out, "dev\n")
		}
		if errOut != "" {
			t.Errorf("fugaro %s wrote %q to stderr, want nothing", arg, errOut)
		}
	}
}

// TestUnknownFlagsStillError: the new flag makes the root accept one more
// spelling, not anything. A flag that is not one of the root's is still
// refused, and the flag stays the root's own -- `fugaro run --version` is not
// `fugaro --version`, and a subcommand that never had the flag still does not.
func TestUnknownFlagsStillError(t *testing.T) {
	for _, args := range [][]string{
		{"--nope"},
		{"-x"},
		{"version", "--json"},   // the subcommand has no --json (and gains none here)
		{"cancel", "--version"}, // the root's flag is the root's, not every command's
	} {
		_, _, err := execute(t, args...)
		if err == nil {
			t.Errorf("fugaro %s succeeded, want an error", strings.Join(args, " "))
			continue
		}
		if !strings.Contains(err.Error(), "unknown") {
			t.Errorf("fugaro %s: err = %v, want an unknown flag error", strings.Join(args, " "), err)
		}
	}
}
