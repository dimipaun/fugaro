package cli

import (
	"regexp"
	"strings"
	"testing"
)

// bareManaged is the retired flag as a whole word: `--managed` and not the
// other flags that begin with the same letters (--managed-settings,
// --managed-deployer). Written once, like bareFirebase, and read from here by
// the two tests below.
var bareManaged = regexp.MustCompile(`--` + retiredManagedFlag + `([^-a-z0-9]|$)`)

// TestSetupManagedFlagIsGone: `fugaro setup --managed` is REMOVED, not
// aliased (R3, D2214). Every creating run records its budget, so the flag has
// nothing left to say, and a no-op flag whose name and help say "record the
// budget" is a lie in the one surface a user reads to learn what a command
// does. Both doors refuse it by name, in one sentence (R3), before anything is
// read — a script that still passes it is told the flag is gone and why,
// rather than being left with pflag's bare "unknown flag".
func TestSetupManagedFlagIsGone(t *testing.T) {
	for door, build := range doorCommands {
		cmd := build()
		if f := cmd.Flags().Lookup(retiredManagedFlag); f != nil {
			t.Errorf("fugaro %s still declares --%s: it is removed, not aliased", door, retiredManagedFlag)
		}
		// The offer, in the two places a door makes one: the flag list it
		// prints, and its Long. A *refusal* may name the retired flag (that is
		// the sentence a scripted run gets, below).
		for _, text := range []string{cmd.Flags().FlagUsages(), cmd.Long} {
			if m := bareManaged.FindAllString(text, -1); len(m) > 0 {
				t.Errorf("fugaro %s's help offers --%s %d time(s); every creating run records its budget", door, retiredManagedFlag, len(m))
			}
		}
	}
	for _, door := range []string{"setup", "update"} {
		firstRunEnv(t)
		out, errOut, err := executeStdin(t, "", door, "--"+retiredManagedFlag)
		s := out + errOut + ErrorText(err)
		if err == nil {
			t.Fatalf("fugaro %s --%s exited 0:\n%s", door, retiredManagedFlag, s)
		}
		if got := ExitCode(err); got != ExitUserError {
			t.Errorf("fugaro %s --%s: exit %d, want %d", door, retiredManagedFlag, got, ExitUserError)
		}
		if !strings.Contains(s, "--"+retiredManagedFlag) {
			t.Errorf("fugaro %s's refusal does not name the flag that is gone:\n%s", door, s)
		}
		if !strings.Contains(s, "records its budget") {
			t.Errorf("fugaro %s's refusal does not say why the flag has nothing left to do:\n%s", door, s)
		}
		if strings.Contains(s, "unknown flag") {
			t.Errorf("fugaro %s's refusal is pflag's own sentence rather than the removal's:\n%s", door, s)
		}
	}
}

// TestUpdateManagedFlagIsGone: the update door's shape of the same removal
// (R3). `--managed` was never update's — the creating flags are fugaro setup's
// and update refuses each by name — and it is gone from the tree rather than
// left declared-and-refused, so the door answers with the removal's one
// sentence instead of a "that is fugaro setup's" that names a flag nobody can
// pass there either.
func TestUpdateManagedFlagIsGone(t *testing.T) {
	firstRunEnv(t)
	out, errOut, err := executeStdin(t, "", "update", "--"+retiredManagedFlag)
	s := out + errOut + ErrorText(err)
	if err == nil {
		t.Fatalf("fugaro update --%s exited 0:\n%s", retiredManagedFlag, s)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d:\n%s", got, ExitUserError, s)
	}
	for _, want := range []string{"--" + retiredManagedFlag, "fugaro setup", "records its budget"} {
		if !strings.Contains(s, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "does not take") {
		t.Errorf("the refusal is the foreign-flag sentence, and the flag is not declared anywhere to be foreign:\n%s", s)
	}
}
