package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// rigor_floor_test.go is G7's half of the `fugaro rigor` group (D1170,
// docs/config-management/implementation-plan.md): `fugaro rigor floor`, the
// command the settings design's SD10 designed and closed as this task's
// duplicate on 2026-11-10.
//
// The one rule these tests hold is the confirmation D1170 asks for, and it is
// the reason this command is the plan's SEC one: the floor is the levels every
// run of a mode must run, so taking one away — by removing a mode's entry or by
// lowering the levels it names — is a whole project reviewing less. That is the
// step `confirmTypedStrict` is for: nothing stands in for the project's name
// typed at a terminal, and the refusal names no flag, because a refusal that
// named one would be read by whoever it refuses as the thing to pass next.
//
// The agent-session fence comes before the confirmation and before the
// document is read (D1026): a coding agent's session is refused the removal
// outright, so the question is never printed to a session and a managed
// project's gate never sees the step at all. Adding a mode the file does not
// carry is the other direction and needs no confirmation: it can only require
// more review, never less.
//
// The world is the settings family's own (settingsWriteHome): the local
// project config the floor lives in, project aurora. The floor is written with
// the editor's own list operations (internal/yamledit), so what the tests read
// back is a document localcfg.Parse accepts — the same loader the write path
// re-reads with.

// rigorFloorHome writes the local project config carrying the floor a test
// starts from, and answers its path.
func rigorFloorHome(t *testing.T, floor string) string {
	t.Helper()
	_, path := settingsWriteHomeWith(t, floor)
	return path
}

// The red step: a removal takes the project's name typed at a terminal, and
// --yes does not stand in for it (D1170, D846). A removal is not a plan a
// document can show — what it deletes is a floor — so the typed name is the
// whole of the decision, and the flag a person types in advance, in a script,
// possibly from an agent, is exactly what the refusal exists for.
//
// The lowering direction is the same rule and is asked here too: taking a level
// out of a mode's floor is a run of that mode reviewing less, and it is refused
// the flag the same way. The other half of the test is the door the removal
// does have: at the owner's own terminal, with the project's name typed, it
// lands — and the file is read back by localcfg's own loader.
func TestRigorFloorRemovalTakesTheProjectNameTyped(t *testing.T) {
	path := rigorFloorHome(t, "review_floor:\n  pro: [l2, l3]\n")
	before := readFileString(t, path)
	noAgentSession(t)
	fakeTerminal(t)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"a removal", []string{"rigor", "floor", "unset", "pro", "--yes"}},
		{"a lowering", []string{"rigor", "floor", "set", "pro", "l2", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The answer on stdin is not the project's name, and --yes is set:
			// a run that took the flag for an answer would write here.
			out, _, err := executeStdin(t, "yes\n", tc.args...)
			if err == nil {
				t.Fatalf("--yes confirmed %s of a review floor:\n%s", tc.name, out)
			}
			if !strings.Contains(err.Error(), "not confirmed") {
				t.Errorf("the refusal is not the typed confirmation's: %v", err)
			}
			if !strings.Contains(out, "CONFIRM") {
				t.Errorf("%s printed no confirmation to refuse:\n%s", tc.name, out)
			}
			if got := readFileString(t, path); got != before {
				t.Fatalf("%s wrote the floor anyway:\n%s", tc.name, got)
			}
		})
	}

	// The door the removal does have: the project's name typed at the owner's
	// own terminal. Nothing else is asked, and the entry goes.
	out, _, err := executeStdin(t, "aurora\n", "rigor", "floor", "unset", "pro")
	if err != nil {
		t.Fatalf("the owner's own terminal was refused a floor removal: %v\n%s", err, out)
	}
	got := readFileString(t, path)
	if strings.Contains(got, "review_floor") {
		t.Errorf("the removal left the mode's floor behind:\n%s", got)
	}
	if _, err := localcfg.Parse([]byte(got)); err != nil {
		t.Errorf("the written file is not a local project config: %v\n%s", err, got)
	}
}

// The fence, and it comes first: a coding agent's session is refused the
// removal and the lowering before the document is read and before any question
// is printed, so the typed name is never asked of a session that cannot answer
// it, and a managed project's gate never sees the step at all (D1026, D1170).
// --yes changes neither.
//
// The check that the confirmation was never printed is the ordering: a fence
// moved after the question would refuse the same run with the same marker in
// the message, and only the absence of the question says which came first.
func TestRigorFloorRefusesAnAgentSession(t *testing.T) {
	path := rigorFloorHome(t, "review_floor:\n  pro: [l2, l3]\n")
	before := readFileString(t, path)
	agentSession(t)
	noTerminal(t)

	for _, args := range [][]string{
		{"rigor", "floor", "unset", "pro", "--yes"},
		{"rigor", "floor", "set", "pro", "l2", "--yes"},
	} {
		out, _, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v: a coding agent's session changed the review floor:\n%s", args, out)
		}
		if !strings.Contains(err.Error(), "coding agent's session") {
			t.Errorf("%v: the refusal does not name the session: %v", args, err)
		}
		if strings.Contains(out, "CONFIRM") || strings.Contains(out, "to confirm:") {
			t.Errorf("%v: the session was asked for the typed name before the fence refused it:\n%s", args, out)
		}
		if got := readFileString(t, path); got != before {
			t.Fatalf("%v: the refused write changed the floor:\n%s", args, got)
		}
	}
}

// The other direction needs no confirmation: a mode the file does not carry is
// an entry ADDED to the floor, and adding a floor can only require more review,
// never less (D1170's own asymmetry). The run asks nothing at a terminal, the
// entry lands beside the ones already there, and the file reads back.
func TestRigorFloorAddsAnEntryWithoutTheTypedForm(t *testing.T) {
	path := rigorFloorHome(t, "review_floor:\n  pro: [l2]\n")
	noAgentSession(t)
	fakeTerminal(t)

	// Empty stdin at a terminal: a run that asked for the project's name would
	// read nothing and refuse.
	out, _, err := executeStdin(t, "", "rigor", "floor", "set", "critical", "l2,l3")
	if err != nil {
		t.Fatalf("adding a floor entry was refused: %v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	got := readFileString(t, path)
	for _, want := range []string{"critical:", "- l2", "- l3", "pro:", "- l2"} {
		if !strings.Contains(got, want) {
			t.Errorf("the added entry is not in the file (%q missing):\n%s", want, got)
		}
	}
	cfg, err := localcfg.Parse([]byte(got))
	if err != nil {
		t.Fatalf("the written file is not a local project config: %v\n%s", err, got)
	}
	if got := cfg.ReviewFloor["critical"]; len(got) != 2 {
		t.Errorf("the floor reads back as %v, want the two levels that were named", cfg.ReviewFloor)
	}
}

// The one removal whose effect a person could read wrong, and the sentence that
// keeps the command honest about it. A config silent about the floor keeps the
// published one — the floor is the member a publish must not lower by accident
// (localcfg's own comment on the field, and buildProjectPolicy) — and the
// editor cannot write the `review_floor: {}` that would take it away, so a
// removal of the file's LAST entry lowers the file and nothing else. The
// command says which of the two it just did, and it says it only where it is
// true: a removal that leaves another mode's floor behind reaches the node at
// the next publish, and warns about nothing.
func TestRigorFloorUnsetOfTheLastEntrySaysWhatItLeaves(t *testing.T) {
	t.Run("the last entry", func(t *testing.T) {
		path := rigorFloorHome(t, "review_floor:\n  pro: [l2]\n")
		noAgentSession(t)
		fakeTerminal(t)

		out, _, err := executeStdin(t, "aurora\n", "rigor", "floor", "unset", "pro")
		if err != nil {
			t.Fatalf("the removal was refused: %v\n%s", err, out)
		}
		if !strings.Contains(out, "names no floor at all") || !strings.Contains(out, "review_floor: {}") {
			t.Errorf("the removal of the file's only floor says nothing about the published one:\n%s", out)
		}
		if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); lines[len(lines)-1] != "next: fugaro update" {
			t.Errorf("the last line is %q, want the one hand-off line", lines[len(lines)-1])
		}
		if got := readFileString(t, path); strings.Contains(got, "review_floor") {
			t.Errorf("the removal left the key behind:\n%s", got)
		}
	})

	t.Run("another mode's floor stays", func(t *testing.T) {
		path := rigorFloorHome(t, "review_floor:\n  pro: [l2]\n  critical: [l3]\n")
		noAgentSession(t)
		fakeTerminal(t)

		out, _, err := executeStdin(t, "aurora\n", "rigor", "floor", "unset", "pro")
		if err != nil {
			t.Fatalf("the removal was refused: %v\n%s", err, out)
		}
		if strings.Contains(out, "names no floor at all") {
			t.Errorf("a removal that leaves another mode's floor behind warns about a floor that is gone:\n%s", out)
		}
		got := readFileString(t, path)
		if !strings.Contains(got, "critical:") {
			t.Errorf("the other mode's floor went with the removal:\n%s", got)
		}
	})
}
