//go:build !ee

package cli

import (
	"strings"
	"testing"
)

// TestChangeSetRefusedByUntaggedBuild: a change set is the Subscription
// Edition's (D616) — the settle step and the ordering that flips a set's pull
// requests ready are ee/ behaviour — so a build without the code refuses the
// flag the way it refuses a paid command, in D522's sentence, at the caller and
// before any cloud call: no execution, no stored task, no token.
//
// The file is tagged because the sentence is a build's own answer, not a
// runtime choice: a tagged binary reaches the same refusal by a different
// sentence (changeSetRefusal, and it keeps the flag refused until the settle
// step ships), and TestChangeSetRefusedUntilSettleExists judges both in
// process. What only this file can judge is that the released, untagged build
// answers the exact sentence a paid name gets.
func TestChangeSetRefusedByUntaggedBuild(t *testing.T) {
	f := changeSetFixture(t)
	_, errOut, err := execute(t, "run", "--change-set", "--repo", "acme/app", "--repo", "acme/other", "Add a feature")
	if err == nil {
		t.Fatal("an untagged build launched a change set")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("the refusal exited %d, want %d: %v", ExitCode(err), ExitUserError, err)
	}
	want := "--change-set is part of Fugaro Subscription Edition; this build does not include it"
	if err.Error() != want {
		t.Errorf("the refusal = %q, want exactly %q", err.Error(), want)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions were started, want none: the flag is judged before the launch reaches the cloud", n)
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("the refused launch stored a task")
	}
	// The refusal is the flag's and not --repo's, so a caller who typed one
	// --repo reads the same sentence rather than a complaint about the count.
	if _, _, err := execute(t, "run", "--change-set", "Add a feature"); err == nil || err.Error() != want {
		t.Errorf("--change-set with one repository = %v, want %q", err, want)
	}
	for _, stream := range []string{errOut} {
		if strings.Contains(strings.ToLower(stream), "licen") {
			t.Errorf("the refusal %q speaks of a licence, which D523 defers", stream)
		}
	}
}
