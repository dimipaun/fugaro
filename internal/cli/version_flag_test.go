package cli

// The root `-v, --version` flag: the conventional spelling of the `version`
// subcommand. The owner typed `fugaro -v` and `fugaro --version` and got
// "unknown shorthand flag: 'v'" / "unknown flag: --version".
//
// The flag is cobra's own (the root command's Version field plus a version
// template), which answers it inside execute, before the root's
// PersistentPreRun -- so it prints the version, exits 0 and runs nothing: no
// checkout read, no local config, no cloud call, no update check. Cobra
// resolves the command before it gets that far, though, and the flag does not
// exist yet when it does, so the argv boundary reduces a leading
// `-v`/`--version` to `--version` alone (NormalizeVersionArgs; cmd/fugaro/main.go
// and these helpers call it) -- which is what keeps `fugaro --version run run`
// from running `run`. The tests below pin each half of that, and the
// byte-for-byte sameness with `fugaro version` that makes the two spellings
// one fact.

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// executeResolved runs args through a fresh tree like execute does, and also
// returns the command cobra resolved and executed: `fugaro --version run run`
// must execute the ROOT command, and only ExecuteContextC's first result can
// show that. Args go through NormalizeArgs exactly as cmd/fugaro/main.go sends
// them, so what these tests exercise is the real pipeline.
func executeResolved(t *testing.T, args ...string) (*cobra.Command, string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(NormalizeArgs(args))
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

// TestVersionFlagBeforeASubcommandDoesNotRunIt: a leading -v/--version is the
// whole command, so nothing after it is resolved or run. The arguments after
// the flag are the ones cobra's own command resolution would otherwise act on
// -- it reads a `--version` it has not declared yet as a flag with a value and
// consumes the token after it -- and each is a command that would say so if it
// were reached: `run run` and `logs logs` would run, and `cancel` takes
// exactly one run ID. All of them must report the root command and the version
// line instead. The two-token cases are the ones that fail if the argv
// boundary stops rewriting, which is why the single-token `cancel` case is not
// the whole test: `--version cancel` alone happens to reach the root anyway,
// by eating `cancel`.
func TestVersionFlagBeforeASubcommandDoesNotRunIt(t *testing.T) {
	for _, args := range [][]string{
		{"--version", "cancel"},
		{"-v", "cancel"},
		{"--version", "run", "run"},
		{"-v", "logs", "logs"},
		{"--version", "junk", "logs"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			executed, out, errOut, err := executeResolved(t, args...)
			if err != nil {
				t.Fatalf("fugaro %s: %v", strings.Join(args, " "), err)
			}
			if out != "dev\n" {
				t.Errorf("fugaro %s printed %q, want %q", strings.Join(args, " "), out, "dev\n")
			}
			if errOut != "" {
				t.Errorf("fugaro %s wrote %q to stderr, want nothing", strings.Join(args, " "), errOut)
			}
			if got := executed.CommandPath(); got != "fugaro" {
				t.Errorf("fugaro %s executed %q, want the root command: the flag is the whole command, nothing after it runs", strings.Join(args, " "), got)
			}
		})
	}
}

// TestNormalizeVersionArgs is the argv rewrite's table: a LEADING -v or
// --version becomes exactly `--version`, so the token after it cannot be read
// as its value. Everything else is left alone, because the flag is the root's
// own, like the built-in -h: after a subcommand it stays the unknown flag it
// is, and -h still wins when it comes first.
func TestNormalizeVersionArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"no args", nil, nil},
		{"long flag alone", []string{"--version"}, []string{"--version"}},
		{"short flag alone", []string{"-v"}, []string{"--version"}},
		{"long flag before a command", []string{"--version", "run", "run"}, []string{"--version"}},
		{"short flag before a command", []string{"-v", "cancel"}, []string{"--version"}},
		{"short flag before another flag", []string{"-v", "--json"}, []string{"--version"}},
		{"the subcommand", []string{"version"}, []string{"version"}},
		{"the subcommand with the flag", []string{"version", "--version"}, []string{"version", "--version"}},
		{"the flag after a command", []string{"run", "--version"}, []string{"run", "--version"}},
		{"help first", []string{"-h", "--version"}, []string{"-h", "--version"}},
		{"after the terminator", []string{"--", "--version"}, []string{"--", "--version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeVersionArgs(tc.in); !slices.Equal(got, tc.want) {
				t.Fatalf("NormalizeVersionArgs(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	// NormalizeArgs is the boundary's whole job: both rewrites, and a leading
	// version flag leaves nothing for the --repo rewrite to matter to.
	if got := NormalizeArgs([]string{"-v", "secrets", "ls", "--repo"}); !slices.Equal(got, []string{"--version"}) {
		t.Errorf("NormalizeArgs([-v secrets ls --repo]) = %q, want [--version]", got)
	}
}

// TestVersionLineIsReadWhenItIsPrinted: the line comes from the Version
// variable when it is printed, not from the value NewRootCmd copied into the
// tree when it built it. Both trees are built before the variable moves, which
// is the order the package's own seams use (useVersion, withVersion,
// releaseBuild and newImagesRigFor move the variable and then run a command):
// a line read from the tree would print the old version here. The new value is
// not a release version, so no update check is started for it.
func TestVersionLineIsReadWhenItIsPrinted(t *testing.T) {
	flagTree, subTree := NewRootCmd(), NewRootCmd()
	old := Version
	Version = "mutation-proof"
	t.Cleanup(func() { Version = old })
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
		args []string
	}{
		{"--version", flagTree, []string{"--version"}},
		{"version", subTree, []string{"version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			tc.cmd.SetOut(&out)
			tc.cmd.SetErr(&errOut)
			tc.cmd.SetArgs(tc.args)
			if _, err := tc.cmd.ExecuteContextC(context.Background()); err != nil {
				t.Fatalf("fugaro %s: %v", tc.name, err)
			}
			if out.String() != "mutation-proof\n" {
				t.Errorf("fugaro %s printed %q, want %q: the Version variable moved after this tree was built", tc.name, out.String(), "mutation-proof\n")
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
		{"run", "--version"},    // and it is only a leading flag that the boundary rewrites
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
