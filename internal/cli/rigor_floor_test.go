package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// rigor_floor_test.go is G7's half of the `fugaro rigor` group (D1170,
// docs/config-management/implementation-plan.md): `fugaro rigor floor`, the
// command the settings design's SD10 designed and closed as this task's
// duplicate on 2026-11-10.
//
// The step these tests hold is the owner's own: a removal and a lowering take a
// level away from every run of a mode, so the edit is Typed — a coding agent's
// session is refused it whatever `--yes` says, the write path prints what it
// widens and what it writes, and no gate ever admits it (D1552, D1555, D1026).
// The typed project name the plan's section puts in front of that step, and
// D1170 recommends with it, is NOT built: docs/no-prompt-policy is the owner's
// answer to it — rule 1, no command may wait for interactive input, "no y/n
// questions, no typing the project name" — the write path this command goes
// through asks nothing for a Typed edit, and the harness that runs every leaf of
// this tree with its stdin closed (noprompt_harness_test.go) is the policy's own
// check. The plan's two named tests for the typed form are reconciled here
// (rigor_floor.go's header carries the reading); what is left to prove is that
// the step asks nothing from either direction, that the fence still comes first,
// and that adding an entry — the direction that can only require more review —
// is the ordinary write it was designed to be.
//
// The world is the settings family's own (settingsWriteHome): the local project
// config the floor lives in, project aurora. The floor is written with the
// editor's own list operations (internal/yamledit), so what the tests read back
// is a document localcfg.Parse accepts — the same loader the write path
// re-reads with.

// rigorFloorHome writes the local project config carrying the floor a test
// starts from, and answers its path.
func rigorFloorHome(t *testing.T, floor string) string {
	t.Helper()
	_, path := settingsWriteHomeWith(t, floor)
	return path
}

// The red step, reconciled. A removal and a lowering are the owner's own step,
// and they ask nothing: not the project's name, not anything else (D1540,
// D1552, D1555). What is asserted is what survives D1170's confirmation — the
// floor moves, and the write says whose step it is — and that the run asks
// nothing while its stdin is a terminal with nobody behind it: a question here
// would read an empty line and refuse, and the harness fails the same banner
// with stdin closed.
//
// The plan named this test after the typed form
// (TestRigorFloorRemovalTakesTheProjectNameTyped). The owner's policy removed
// the form, so the name went with it; the subject — the floor a removal and a
// lowering move, and the fence around them — is the test below.
func TestRigorFloorRemovalAndLoweringAskNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"a removal", []string{"rigor", "floor", "unset", "pro"}, nil},
		{"a lowering", []string{"rigor", "floor", "set", "pro", "l2"}, []string{"l2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := rigorFloorHome(t, "review_floor:\n  pro: [l2, l3]\n")
			noAgentSession(t)
			fakeTerminal(t)

			out, _, err := executeStdin(t, "", tc.args...)
			if err != nil {
				t.Fatalf("%s was refused: %v\n%s", tc.name, err, out)
			}
			assertNoPrompt(t, out)
			if !strings.Contains(out, "the owner's own step") {
				t.Errorf("%s printed no sentence saying whose step it is:\n%s", tc.name, out)
			}
			got := readFileString(t, path)
			cfg, err := localcfg.Parse([]byte(got))
			if err != nil {
				t.Fatalf("the written file is not a local project config: %v\n%s", err, got)
			}
			if floor := cfg.ReviewFloor["pro"]; !slices.Equal(floor, tc.want) {
				t.Errorf("%s left pro's floor at %v, want %v", tc.name, floor, tc.want)
			}
		})
	}
}

// The fence, and it comes first: a coding agent's session is refused a removal
// and a lowering before the document is read, so the session is shown neither
// the file's diff nor the sentence the write would print, and a managed
// project's gate never sees the step at all (D1026, D1552, D1555). --yes
// changes neither. What the refusal hands back instead is the command the person
// runs at their own terminal.
//
// The diff's absence is the ordering, and it is the whole of what this fence
// adds to the write path's own refusal: applySettingsEdit refuses the same run
// with the same marker in the message, and prints the document beside it, so
// only the missing diff says which of the two refused.
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
		if !strings.Contains(err.Error(), "fugaro rigor floor") {
			t.Errorf("%v: the refusal does not name the command the person runs instead: %v", args, err)
		}
		assertNoPrompt(t, out)
		if strings.Contains(out, "  - ") || strings.Contains(out, "  + ") {
			t.Errorf("%v: the session was shown the document's diff, so the fence did not come before the read:\n%s", args, out)
		}
		if got := readFileString(t, path); got != before {
			t.Fatalf("%v: the refused write changed the floor:\n%s", args, got)
		}
	}
}

// The other direction: a mode the file does not carry is an entry ADDED to the
// floor, and adding a floor can only require more review, never less (D1170's
// own asymmetry). The run asks nothing, the entry lands beside the ones already
// there, and the file reads back — through localcfg's own loader, which runs
// policy.ParseFloor over the key.
func TestRigorFloorAddsAnEntryAskingNothing(t *testing.T) {
	path := rigorFloorHome(t, "review_floor:\n  pro: [l2]\n")
	noAgentSession(t)
	fakeTerminal(t)

	// Empty stdin at a terminal: a run that asked anything would read nothing.
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
	if floor := cfg.ReviewFloor["critical"]; len(floor) != 2 {
		t.Errorf("the floor reads back as %v, want the two levels that were named", cfg.ReviewFloor)
	}
}

// A set naming the floor that is already there, and the one case that is
// written rather than reported. The levels a person types are put in the
// vocabulary's own order before anything is compared, so how they typed them
// does not matter; the order of the FILE's own entry is the comparison's other
// half, and a hand-written one that names the levels the other way round is the
// floor as much as a canonical one is — the entry comes back in the vocabulary's
// order, which is the one thing a set may write for a floor it does not change.
func TestRigorFloorSetOfTheFloorThatIsThere(t *testing.T) {
	t.Run("the argument in another order", func(t *testing.T) {
		path := rigorFloorHome(t, "review_floor:\n  pro: [l2, l3]\n")
		before := readFileString(t, path)
		noAgentSession(t)
		fakeTerminal(t)

		out, _, err := executeStdin(t, "", "rigor", "floor", "set", "pro", "l3,l2")
		if err != nil {
			t.Fatalf("a set naming the floor that is there was refused: %v\n%s", err, out)
		}
		assertNoPrompt(t, out)
		if !strings.Contains(out, "already carries the floor that was asked for") {
			t.Errorf("the no-op does not say the floor is already there:\n%s", out)
		}
		if got := readFileString(t, path); got != before {
			t.Errorf("a set naming the floor that is there wrote the file:\n%s", got)
		}
	})

	t.Run("the file's entry in another order", func(t *testing.T) {
		path := rigorFloorHome(t, "review_floor:\n  pro: [l3, l2]\n")
		noAgentSession(t)
		fakeTerminal(t)

		out, _, err := executeStdin(t, "", "rigor", "floor", "set", "pro", "l2,l3")
		if err != nil {
			t.Fatalf("the set was refused: %v\n%s", err, out)
		}
		got := readFileString(t, path)
		cfg, err := localcfg.Parse([]byte(got))
		if err != nil {
			t.Fatalf("the written file is not a local project config: %v\n%s", err, got)
		}
		if floor := cfg.ReviewFloor["pro"]; !slices.Equal(floor, []string{"l2", "l3"}) {
			t.Errorf("the entry reads back as %v, want the levels in the vocabulary's order:\n%s", floor, got)
		}
	})
}

// The empty argument is the one way to name no level at all, and it is refused
// with the sentence pointing at the command that removes a floor: a person who
// wants a mode to have no floor is not asking for this one.
func TestRigorFloorRefusesAnEmptyLevelList(t *testing.T) {
	path := rigorFloorHome(t, "review_floor:\n  pro: [l2]\n")
	before := readFileString(t, path)
	noAgentSession(t)
	fakeTerminal(t)

	out, _, err := executeStdin(t, "", "rigor", "floor", "set", "pro", "")
	if err == nil {
		t.Fatalf("an empty LEVELS was accepted:\n%s", out)
	}
	if !strings.Contains(err.Error(), "no review level was named") || !strings.Contains(err.Error(), "fugaro rigor floor unset") {
		t.Errorf("the refusal names neither the mistake nor the command that removes a floor: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused run wrote the floor:\n%s", got)
	}
}

// A floor a person wrote as one flow mapping cannot be edited at all, and the
// refusal owes the person the two things this one carries: the shape the editor
// will not touch and the way out of it (block style, one item per line). The
// editor refuses to walk into a flow collection, so both directions meet it and
// both write nothing.
func TestRigorFloorRefusesAFloorWrittenAsOneFlowMapping(t *testing.T) {
	path := rigorFloorHome(t, "review_floor: {pro: [l2, l3]}\n")
	before := readFileString(t, path)
	noAgentSession(t)
	fakeTerminal(t)

	for _, args := range [][]string{
		{"rigor", "floor", "unset", "pro"},
		{"rigor", "floor", "set", "pro", "l2"},
	} {
		out, _, err := executeStdin(t, "", args...)
		if err == nil {
			t.Fatalf("%v: a floor written as one flow mapping was edited:\n%s", args, out)
		}
		if !strings.Contains(err.Error(), "cannot be changed by one edit") || !strings.Contains(err.Error(), "block style") {
			t.Errorf("%v: the refusal names neither the shape nor the way out: %v", args, err)
		}
		if got := readFileString(t, path); got != before {
			t.Fatalf("%v: the refused edit wrote the document:\n%s", args, got)
		}
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

		out, _, err := executeStdin(t, "", "rigor", "floor", "unset", "pro")
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

		out, _, err := executeStdin(t, "", "rigor", "floor", "unset", "pro")
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
