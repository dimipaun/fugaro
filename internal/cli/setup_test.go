package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// setup_test.go pins the two front doors task W1.2 of
// docs/setup-wizard/implementation-plan.md registers: `fugaro setup` is the
// interactive first-time wizard and `fugaro update` is the re-converge after a
// CLI upgrade, both built by the one shared converge-command constructor
// (init.go's newInitCmdWith), and each taking the flags that belong to it. A
// flag that only makes sense in one door is refused in the other, naming the
// door that owns it — which is the whole of what "the flags that belong to
// them" means, and it is a table here rather than a sentence in the help.
//
// The stage sets, the wizard's questions and update's refusals are later
// tasks' (W1.3 and W5.1); what this file owns is that the two doors exist,
// that neither is the other, and that neither one's flags leak into it.

// doorCommands is the two front doors by the name a user types, and the
// constructor the command tree has to have registered.
var doorCommands = map[string]func() *cobra.Command{
	"setup":  newSetupCmd,
	"update": newUpdateCmd,
}

// TestSetupAndUpdateAreRegistered: both doors are top-level commands of the
// real tree, both are visible (unlike the tombstone), and each is the command
// its own constructor builds.
func TestSetupAndUpdateAreRegistered(t *testing.T) {
	root := NewRootCmd()
	for name, build := range doorCommands {
		c, _, err := root.Find([]string{name})
		if err != nil || c == root || c.Name() != name {
			t.Errorf("fugaro %s is not registered: %v", name, err)
			continue
		}
		if c.Hidden {
			t.Errorf("fugaro %s is hidden: it is one of the two front doors", name)
		}
		if c.RunE == nil {
			t.Errorf("fugaro %s has no run", name)
		}
		want := build()
		if c.Use != want.Use || c.Short != want.Short {
			t.Errorf("fugaro %s is not %T's command: %q/%q, want %q/%q", name, want, c.Use, c.Short, want.Use, want.Short)
		}
		if c.Short == "" || c.Long == "" {
			t.Errorf("fugaro %s has no help of its own", name)
		}
	}
	// The two are different commands, with different names: a copy-paste that
	// left both doors with one Use would make `fugaro update` unreachable.
	if setup, update := newSetupCmd(), newUpdateCmd(); setup.Use == update.Use {
		t.Errorf("both doors are %q", setup.Use)
	}
}

// declaredFlags is every flag a door declares, hidden or not: what it takes
// from a command line. The doors differ only in what they *hide* — setup hides
// --forget, update hides the six creating flags, each refused by name — so the
// declared sets are the converge's flags, and they have to be the same set.
func declaredFlags(door string) map[string]bool {
	out := map[string]bool{}
	doorCommands[door]().Flags().VisitAll(func(f *pflag.Flag) { out[f.Name] = true })
	return out
}

// sharedFlagsFloor names flags the converge cannot be without. It is not the
// check below — that one reads the two doors' own flag sets — but the floor
// under it: two doors that lost the same flag at once would leave the two sets
// equal to each other and empty of it.
var sharedFlagsFloor = []string{
	"repo", "firebase", "yes", "plan-only", "print-vars", "config-only",
	"json", "anchor", "retire", "gcp-project", "region", "name",
}

// TestBothDoorsTakeTheSharedFlags: the flags that belong to the converge are
// on both doors, and they are *declared* there rather than hidden: a user
// reading `fugaro update --help` has to find --repo, --firebase and --yes.
//
// The two doors' declared sets are read off the commands and held to each
// other rather than compared against a list written here. A list cannot say
// this: a flag missing from it is a flag nothing checks, which is how --base,
// --expect-digest, --image-source and --replace-image — all four declared by
// both doors — went unchecked, so a door that dropped one passed.
func TestBothDoorsTakeTheSharedFlags(t *testing.T) {
	setup, update := declaredFlags("setup"), declaredFlags("update")
	for _, d := range []struct {
		door, other string
		has, lacks  map[string]bool
	}{{"setup", "update", setup, update}, {"update", "setup", update, setup}} {
		for flag := range d.has {
			if !d.lacks[flag] {
				t.Errorf("fugaro %s declares --%s and fugaro %s does not: they are two doors of one converge, and a flag lost from one of them converges something else", d.door, flag, d.other)
			}
		}
	}
	for _, flag := range sharedFlagsFloor {
		for name, build := range doorCommands {
			f := build().Flags().Lookup(flag)
			if f == nil {
				t.Errorf("fugaro %s has no --%s", name, flag)
				continue
			}
			if f.Hidden {
				t.Errorf("fugaro %s hides --%s, which it takes", name, flag)
			}
		}
	}
}

// TestAForeignFlagIsHiddenFromItsDoorsHelp: the flag a door refuses is
// *declared* on it — so a run that passes one gets the door's sentence naming
// the door that owns it, and not cobra's bare "unknown flag" — and hidden, so
// neither the help nor the flag list offers a flag the door will not take.
// Both halves matter and they pull in opposite directions: without the
// declaration the refusal cannot name anything, and without the hiding
// `fugaro update --help` advertises --create-project on the command that
// refuses it.
func TestAForeignFlagIsHiddenFromItsDoorsHelp(t *testing.T) {
	for _, tc := range []struct{ door, flag string }{
		{"setup", "forget"},
		{"update", "create-project"},
		{"update", "display-name"},
		{"update", "parent"},
		{"update", "managed"},
		{"update", "link-billing"},
		{"update", "provision-batch"},
	} {
		t.Run(tc.door+" --"+tc.flag, func(t *testing.T) {
			cmd := doorCommands[tc.door]()
			f := cmd.Flags().Lookup(tc.flag)
			if f == nil {
				t.Fatalf("fugaro %s declares no --%s: the refusal would be cobra's, which names no door", tc.door, tc.flag)
			}
			if !f.Hidden {
				t.Errorf("fugaro %s's --%s is not hidden, and the door refuses it", tc.door, tc.flag)
			}
			// The flag list, not the whole help: a line that *is* the flag is
			// the offer, where a mention of it inside another flag's usage
			// text is not (--non-interactive's says what it does "with
			// --forget and --config-only").
			for _, line := range strings.Split(cmd.Flags().FlagUsages(), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "--"+tc.flag+" ") {
					t.Errorf("fugaro %s's flag list offers --%s, which it refuses:\n%s", tc.door, tc.flag, strings.TrimSpace(line))
				}
			}
			// The Long as well, which the flag list cannot speak for: setup's
			// help went on telling the reader what --forget does — twice —
			// after the flag itself was hidden, so the generated reference
			// printed the refusal and the offer side by side, and nothing here
			// noticed.
			//
			// A paragraph that *begins* with the flag is a paragraph about it,
			// which is the offer in prose. A door may still name a flag it
			// refuses inside a sentence — update's Long lists the creating
			// flags as the ones it refuses by name, which is the sentence that
			// says it will not take them — so only the paragraph's opening
			// counts.
			for _, para := range strings.Split(cmd.Long, "\n\n") {
				if strings.HasPrefix(strings.TrimSpace(para), "--"+tc.flag+" ") {
					t.Errorf("fugaro %s's help has a paragraph documenting --%s, which it refuses:\n%s", tc.door, tc.flag, strings.TrimSpace(para))
				}
			}
		})
	}
}

// TestSetupRefusesAnUpdateOnlyFlag: a flag that only makes sense once an
// installation exists is `fugaro update`'s, and `fugaro setup` — the first
// run — refuses it by name rather than ignoring it. `--forget` is that flag:
// it unwinds log isolation and registry cleanup and takes every address out of
// Terraform's state, which a first run has nothing to do.
func TestSetupRefusesAnUpdateOnlyFlag(t *testing.T) {
	firstRunEnv(t)
	out, errOut, err := executeStdin(t, "", "setup", "--forget", "--yes")
	if err == nil {
		t.Fatal("fugaro setup --forget exited 0")
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(got, "--forget") || !strings.Contains(got, "fugaro update") {
		t.Errorf("the refusal does not name the flag and the door that owns it: %q", got)
	}
	if strings.Contains(got, "fugaro setup --forget") {
		t.Errorf("the refusal offers the flag on the door that refuses it: %q", got)
	}
}

// TestUpdateRefusesASetupOnlyFlag is the mirror: everything that creates is
// `fugaro setup`'s — the project, its display name and parent, the managed
// flag that goes with them, the billing link, and the Batch backend's
// provisioning, which rides a stage `update` does not register. `fugaro
// update` creates nothing, so each is refused by name. Each flag is passed
// the way its own type takes it, so the refusal under test is the door's and
// not cobra's "invalid argument".
func TestUpdateRefusesASetupOnlyFlag(t *testing.T) {
	firstRunEnv(t)
	for _, tc := range []struct{ name, arg string }{
		{"create-project", "--create-project"},
		{"display-name", "--display-name=acme"},
		{"parent", "--parent=folders/123"},
		{"managed", "--managed"},
		{"link-billing", "--link-billing=0123AB-4567CD-89EF01"},
		{"provision-batch", "--provision-batch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := executeStdin(t, "", "update", tc.arg, "--yes")
			if err == nil {
				t.Fatalf("fugaro update %s exited 0", tc.arg)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			got := out + errOut + ErrorText(err)
			if !strings.Contains(got, "--"+tc.name) || !strings.Contains(got, "fugaro setup") {
				t.Errorf("the refusal does not name the flag and the door that owns it: %q", got)
			}
		})
	}
}

// TestUpdateRefusesAnArgument: `update` takes no path, unlike `upgrade
// [PATH...]`, whose argument is a checkout to pin and refresh. The converge
// works from the selected project and the current directory, so an argument is
// a typo (or the wrong command) and must be refused rather than ignored.
func TestUpdateRefusesAnArgument(t *testing.T) {
	firstRunEnv(t)
	dir := t.TempDir()
	_, _, err := executeStdin(t, "", "update", dir)
	if err == nil {
		t.Fatal("fugaro update <path> exited 0")
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	if !strings.Contains(ErrorText(err), "update") {
		t.Errorf("the refusal does not name the command: %v", err)
	}
	// `setup` does take one: with --repo, the checkout to onboard.
	if _, _, err := executeStdin(t, "", "setup", dir); err == nil {
		t.Error("fugaro setup <path> without --repo exited 0")
	} else if !strings.Contains(ErrorText(err), "--repo") {
		t.Errorf("fugaro setup's argument refusal does not name --repo: %v", err)
	}
}
