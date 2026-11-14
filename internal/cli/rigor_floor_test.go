package cli

import (
	"encoding/json"
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
		{"rigor", "floor", "unset", "pro"},
		{"rigor", "floor", "set", "pro", "l2"},
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
//
// The classification is asserted and not assumed: an edit that takes a level
// away is Typed, and the write path prints the sentence saying what it widens
// for a Typed one and the sentence naming the edit for every other (the
// `else if e.Typed` branch of applySettingsEdit). So the widening sentence's
// ABSENCE is what pins an addition out of that class — a run that read it here
// would be a run that classed an addition as a step the owner has to take.
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
	if !strings.Contains(out, "set review_floor.critical from none to l2, l3 in") {
		t.Errorf("the addition does not name itself as the ordinary write it is:\n%s", out)
	}
	if strings.Contains(out, "whole project reviewing less") {
		t.Errorf("an addition was classed as the owner's own step, which is the class a lowering and a removal are in:\n%s", out)
	}
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

// The three arguments refused before the document is read: a mode the rigor
// vocabulary does not carry, a level the policy vocabulary does not carry, and
// a level named twice. The first two are the loaders' own rules — rigor.Known
// and policy.ParseFloor refuse the same names — so a file carrying them could
// not be read back either; the third is the set-shaped argument's own, since a
// floor is a set and naming one level twice asks for nothing the once does.
// Each refusal names the offending word and the vocabulary to pick from, says
// nothing was written, and leaves the file as it was.
func TestRigorFloorRefusesAnArgumentOutsideTheVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"a mode the vocabulary does not carry", []string{"rigor", "floor", "set", "fast", "l2"}, `"fast" is not a rigor mode`},
		{"a mode the vocabulary does not carry, on a removal", []string{"rigor", "floor", "unset", "fast"}, `"fast" is not a rigor mode`},
		{"a level the vocabulary does not carry", []string{"rigor", "floor", "set", "pro", "l9"}, `"l9" is not a review level`},
		{"a level named twice", []string{"rigor", "floor", "set", "pro", "l2,l2"}, "review level l2 is listed twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := rigorFloorHome(t, "review_floor:\n  pro: [l2]\n")
			before := readFileString(t, path)
			noAgentSession(t)
			fakeTerminal(t)

			out, _, err := executeStdin(t, "", tc.args...)
			if err == nil {
				t.Fatalf("%v was accepted:\n%s", tc.args, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v: the refusal does not say %q: %v", tc.args, tc.want, err)
			}
			if !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("%v: the refusal does not say nothing was written: %v", tc.args, err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("%v: the refused run wrote the document:\n%s", tc.args, got)
			}
		})
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

// The one removal whose spelling decides what a publish does. A file that says
// nothing about `review_floor` keeps the node's floor — the floor is the member
// a publish must not lower by accident (localcfg's own comment on the field, and
// buildProjectPolicy) — so deleting the key would make this command's own name
// false: `unset MODE` would leave the published floor standing. The removal of
// the file's LAST mode therefore writes `review_floor: {}`, the floor of
// nothing, which is the one spelling that takes it away
// (docs/settings-commands/spec.md: the empty mapping is what a removal leaves).
//
// The file reads back through localcfg's loader with a NON-NIL and empty floor,
// which is the value that distinction is made of; a removal that leaves another
// mode's floor behind still writes that mode out and leaves the key a mapping.
func TestRigorFloorUnsetOfTheLastModeKeepsTheEmptyFloor(t *testing.T) {
	t.Run("the last mode", func(t *testing.T) {
		path := rigorFloorHome(t, "review_floor:\n  pro: [l2]\n")
		noAgentSession(t)
		fakeTerminal(t)

		out, _, err := executeStdin(t, "", "rigor", "floor", "unset", "pro")
		if err != nil {
			t.Fatalf("the removal was refused: %v\n%s", err, out)
		}
		if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); lines[len(lines)-1] != "next: fugaro update" {
			t.Errorf("the last line is %q, want the one hand-off line", lines[len(lines)-1])
		}
		got := readFileString(t, path)
		if !strings.Contains(got, "review_floor: {}") {
			t.Errorf("the removal of the file's last mode did not leave the empty mapping:\n%s", got)
		}
		cfg, err := localcfg.Parse([]byte(got))
		if err != nil {
			t.Fatalf("the written file is not a local project config: %v\n%s", err, got)
		}
		if cfg.ReviewFloor == nil {
			t.Errorf("the key went with the removal, and an absent key is the spelling that KEEPS the published floor:\n%s", got)
		}
		if len(cfg.ReviewFloor) != 0 {
			t.Errorf("the removal left %v, want the floor of nothing", cfg.ReviewFloor)
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
		got := readFileString(t, path)
		if strings.Contains(got, "pro:") {
			t.Errorf("the removed mode's entry stayed behind:\n%s", got)
		}
		if !strings.Contains(got, "critical:") {
			t.Errorf("the other mode's floor went with the removal:\n%s", got)
		}
		cfg, err := localcfg.Parse([]byte(got))
		if err != nil {
			t.Fatalf("the written file is not a local project config: %v\n%s", err, got)
		}
		if floor := cfg.ReviewFloor["critical"]; !slices.Equal(floor, []string{"l3"}) {
			t.Errorf("critical's floor reads back as %v, want l3", floor)
		}
	})
}

// The write that has to be able to follow that removal: the empty mapping is
// the one flow collection this command opens, and the first entry written back
// into it is why the editor carries the operation. No member can be spliced
// into `{}`, so a set that met it without opening the key first would leave the
// file unchangeable from then on — the round trip `unset` then `set` is the
// only way back to a floor once a file has one.
func TestRigorFloorSetAfterTheEmptyFloorWritesTheFirstEntryBack(t *testing.T) {
	path := rigorFloorHome(t, "review_floor: {}\n")
	noAgentSession(t)
	fakeTerminal(t)

	out, _, err := executeStdin(t, "", "rigor", "floor", "set", "pro", "l2,l3")
	if err != nil {
		t.Fatalf("a set after the empty floor was refused: %v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	got := readFileString(t, path)
	cfg, err := localcfg.Parse([]byte(got))
	if err != nil {
		t.Fatalf("the written file is not a local project config: %v\n%s", err, got)
	}
	if floor := cfg.ReviewFloor["pro"]; !slices.Equal(floor, []string{"l2", "l3"}) {
		t.Errorf("the first entry written back reads as %v, want l2, l3:\n%s", floor, got)
	}
}

// The read, and the two ways a file can name no floor, which are the whole
// reason it exists: an absent key keeps the published floor and a written `{}`
// takes it away, so a read that showed both as "no floor" would leave the
// person guessing what their next publish does. The text read says which of the
// three spellings the file has; `--json` says it in the value, where the absent
// one is null and the empty one is an empty mapping.
func TestRigorFloorShowPrintsTheFloorPerMode(t *testing.T) {
	t.Run("the file writes a floor", func(t *testing.T) {
		rigorFloorHome(t, "review_floor:\n  critical: [l3]\n  pro: [l2, l3]\n")
		noAgentSession(t)
		noTerminal(t)

		out, _, err := execute(t, "rigor", "floor", "show")
		if err != nil {
			t.Fatalf("the read was refused: %v\n%s", err, out)
		}
		// The rows come out in the vocabulary's order and not the file's.
		pro, critical := strings.Index(out, "pro "), strings.Index(out, "critical ")
		if pro < 0 || critical < 0 || pro > critical {
			t.Errorf("the rows are not the vocabulary's own order:\n%s", out)
		}
		if !strings.Contains(out, "l2, l3") {
			t.Errorf("the read does not print the levels:\n%s", out)
		}

		out, _, err = execute(t, "rigor", "floor", "show", "--json")
		if err != nil {
			t.Fatalf("the read was refused: %v\n%s", err, out)
		}
		var doc rigorFloorDoc
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("--json is not a document: %v\n%s", err, out)
		}
		if doc.State != rigorFloorStateWritten || doc.Project != "aurora" {
			t.Errorf("--json says state %q for project %q, want the written floor of aurora", doc.State, doc.Project)
		}
		if !slices.Equal(doc.ReviewFloor["pro"], []string{"l2", "l3"}) {
			t.Errorf("--json carries %v, want the file's own floor", doc.ReviewFloor)
		}
	})

	t.Run("the file writes the empty mapping", func(t *testing.T) {
		rigorFloorHome(t, "review_floor: {}\n")
		noAgentSession(t)
		noTerminal(t)

		out, _, err := execute(t, "rigor", "floor", "show")
		if err != nil {
			t.Fatalf("the read was refused: %v\n%s", err, out)
		}
		if !strings.Contains(out, "the published floor is taken away") {
			t.Errorf("the read does not say what a written `{}` does at publish:\n%s", out)
		}

		out, _, err = execute(t, "rigor", "floor", "show", "--json")
		if err != nil {
			t.Fatalf("the read was refused: %v\n%s", err, out)
		}
		var doc rigorFloorDoc
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("--json is not a document: %v\n%s", err, out)
		}
		if doc.State != rigorFloorStateEmpty {
			t.Errorf("--json says state %q, want the empty spelling told apart from the absent one", doc.State)
		}
		if doc.ReviewFloor == nil || len(doc.ReviewFloor) != 0 {
			t.Errorf("--json carries %v, want the empty mapping", doc.ReviewFloor)
		}
	})

	t.Run("the file says nothing about the key", func(t *testing.T) {
		rigorFloorHome(t, "commit_name: Fugaro\n")
		noAgentSession(t)
		noTerminal(t)

		out, _, err := execute(t, "rigor", "floor", "show")
		if err != nil {
			t.Fatalf("the read was refused: %v\n%s", err, out)
		}
		if !strings.Contains(out, "the published floor stands") {
			t.Errorf("the read does not say that an absent key keeps the published floor:\n%s", out)
		}

		out, _, err = execute(t, "rigor", "floor", "show", "--json")
		if err != nil {
			t.Fatalf("the read was refused: %v\n%s", err, out)
		}
		var doc rigorFloorDoc
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("--json is not a document: %v\n%s", err, out)
		}
		if doc.State != rigorFloorStateAbsent || doc.ReviewFloor != nil {
			t.Errorf("--json says state %q with %v, want the absent spelling and a null floor", doc.State, doc.ReviewFloor)
		}
	})
}
