package cli

// The persistent --verbose (-v): the flag plumbing CL4 adds, and the reporter
// constructor a command builds from it (D1353, D1354). Nothing here changes a
// command's output: the flag is accepted and read, and the commands that print
// through the reporter do so in later tasks.

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestLeadingDashVIsStillTheVersion: the persistent --verbose takes the
// shorthand cobra would otherwise give the root's version flag, so the only
// thing keeping a leading `fugaro -v` the version is NormalizeVersionArgs'
// rewrite at the argv boundary (F4, D1353). A leading -v answers the version
// and runs nothing -- `-v update` must not converge.
func TestLeadingDashVIsStillTheVersion(t *testing.T) {
	for _, args := range [][]string{{"-v"}, {"-v", "update"}} {
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
				t.Errorf("fugaro %s executed %q, want the root command: the leading flag is the whole command", strings.Join(args, " "), got)
			}
		})
	}
}

// TestVerboseShorthandOnASubcommand: the flag is persistent, so a subcommand
// takes -v -- `fugaro update -v` is the documented form, because a leading
// `fugaro -v update` is the version. Parsing is what this pins for update: it
// is a converge command, and a test that ran it would reach the cloud. The
// same shorthand on a subcommand that runs nothing (version) is checked end to
// end, so a tree where -v resolved to nothing at all cannot pass here.
func TestVerboseShorthandOnASubcommand(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"update"})
	if err != nil {
		t.Fatalf("fugaro update does not resolve in the tree: %v", err)
	}
	if err := cmd.ParseFlags([]string{"-v"}); err != nil {
		t.Fatalf("fugaro update -v: %v", err)
	}
	if v, err := cmd.Flags().GetBool("verbose"); err != nil || !v {
		t.Fatalf("fugaro update -v parsed but --verbose is %v (err %v), want true", v, err)
	}

	out, _, err := execute(t, "version", "-v")
	if err != nil {
		t.Fatalf("fugaro version -v: %v", err)
	}
	if out != "dev\n" {
		t.Errorf("fugaro version -v printed %q, want %q", out, "dev\n")
	}
}

// TestRecipesVerboseKeepsItsMeaning: `recipes ls --verbose` is a different,
// local flag -- it prints each recipe's use_when and steps -- and it keeps
// that meaning now that the root has a persistent --verbose of its own
// (D1353). The local flag shadows the persistent one, which is why it gets the
// shorthand too: a local flag with no shorthand would take the whole
// persistent flag with it and leave `recipes ls -v` unknown.
func TestRecipesVerboseKeepsItsMeaning(t *testing.T) {
	newCloudFixture(t)
	long, _, err := execute(t, "recipes", "ls", "--verbose")
	if err != nil {
		t.Fatalf("fugaro recipes ls --verbose: %v", err)
	}
	if !strings.Contains(long, "use when:") || !strings.Contains(long, "steps:") {
		t.Fatalf("fugaro recipes ls --verbose printed %q, want each recipe's use_when and steps", long)
	}
	short, _, err := execute(t, "recipes", "ls", "-v")
	if err != nil {
		t.Fatalf("fugaro recipes ls -v: %v", err)
	}
	if short != long {
		t.Errorf("fugaro recipes ls -v and --verbose differ, so the shorthand is not the local flag:\n-v:        %q\n--verbose: %q", short, long)
	}
	plain, _, err := execute(t, "recipes", "ls")
	if err != nil {
		t.Fatalf("fugaro recipes ls: %v", err)
	}
	if plain == long {
		t.Error("fugaro recipes ls prints what --verbose prints: the local flag lost its meaning")
	}

	local := newRecipesLsCmd().Flags().Lookup("verbose")
	persistent := NewRootCmd().PersistentFlags().Lookup("verbose")
	if local == nil || persistent == nil {
		t.Fatalf("recipes ls --verbose = %v, root --verbose = %v", local, persistent)
	}
	if local.Usage == persistent.Usage {
		t.Errorf("recipes ls --verbose carries the persistent flag's help (%q): the local flag is the one that means use_when and steps", local.Usage)
	}
}

// TestEveryCommandAcceptsVerbose: the flag is the root's and persistent, so
// every command in the tree takes --verbose, and -v with it. The walk is the
// tree's, hidden commands and cobra's own included, so a command added later
// cannot miss the flag without failing here.
func TestEveryCommandAcceptsVerbose(t *testing.T) {
	every := cliRefEveryCommand(cliRefRoot())
	if len(every) < 2 {
		t.Fatal("the walk found no commands, so a pass would prove nothing")
	}
	for _, path := range slices.Sorted(maps.Keys(every)) {
		cmd := every[path]
		f := cmd.Flags().Lookup("verbose")
		if f == nil {
			t.Errorf("`%s` does not accept --verbose: the flag is on the root and persistent, so every command takes it", path)
			continue
		}
		if f.Shorthand != "v" {
			t.Errorf("`%s`'s --verbose has shorthand %q, want \"v\"", path, f.Shorthand)
		}
		if got := cmd.Flags().ShorthandLookup("v"); got == nil || got.Name != "verbose" {
			t.Errorf("`%s`'s -v does not stand for --verbose", path)
		}
	}
}
