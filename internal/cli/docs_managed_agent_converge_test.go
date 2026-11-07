package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The managed agent converge folder's own pin (M9). Two things a reader of the
// folder needs are held here: that its status lines say the build landed, and
// that its handoff note names, per command, which of the plan's §2 handoff rows
// has landed — the note is the only place the three other designs' owners read
// that, and a note that drifts from the tree is the handoff nobody gets.
const (
	macDocReadme   = "docs/managed-agent-converge/README.md"
	macDocPlan     = "docs/managed-agent-converge/implementation-plan.md"
	macDocHandoffs = "docs/managed-agent-converge/handoffs.md"
	macDocSkill    = "plugin/skills/setup/reference/managed.md"
)

// macDocCommandLine returns the handoff note's one line for a command, so a
// case reads that line rather than a substring the whole note happens to carry.
func macDocCommandLine(t *testing.T, doc, command string) string {
	t.Helper()
	found := ""
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "- `"+command+"`") {
			continue
		}
		if found != "" {
			t.Fatalf("%s: two lines open with `%s`; this case reads one", macDocHandoffs, command)
		}
		found = line
	}
	if found == "" {
		t.Fatalf("%s: no line opens with `%s`; the note owes one line per command of §3.1 of the spec", macDocHandoffs, command)
	}
	return found
}

// TestDocsManagedAgentConvergeSaysTheBuildLanded: the folder is a design whose
// build shipped, and a reader who opens it first reads the status line. The one
// property the folder must keep whatever else changes — that a project missing
// either mark keeps D55's refusal, held by TestManagedZeroValueKeepsEveryRefusal
// — is pinned here with it, because the status line is where a reader checks it.
func TestDocsManagedAgentConvergeSaysTheBuildLanded(t *testing.T) {
	for _, path := range []string{macDocReadme, macDocPlan, macDocHandoffs} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s does not exist: %v", path, err)
		}
	}
	readme := flattenDoc(readDoc(t, macDocReadme))
	for _, want := range []string{
		"**Status: a design, approved, and built.",
		// The gate's set is the owner's answer, and the build's own record of
		// which rows landed is the note beside this file.
		"[handoffs.md](handoffs.md)",
		// The refusal outside a managed project is unchanged, and the test
		// that holds it is named rather than described.
		"TestManagedZeroValueKeepsEveryRefusal",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q: the folder's status line is what a reader checks the build against", macDocReadme, want)
		}
	}
	plan := flattenDoc(readDoc(t, macDocPlan))
	for _, want := range []string{
		"**Status: a design, and the build has landed.",
		// The plan's §6 recommends a slot the release moved past, and the
		// version the release notes carry is the one a reader re-runs.
		"is 0.18.0, the release this build ships in",
		"[handoffs.md](handoffs.md)",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q: the plan's status line and its slot are what a later task re-reads", macDocPlan, want)
		}
	}
}

// TestDocsManagedAgentConvergeHandoffsMatchTheTree: the note says, per command
// of the spec's §3.1, whether its handoff row has landed, and the tree is what
// decides. A row that landed and whose command is not in the gate's set, and a
// row that has not landed and whose command a session can already run, are the
// two mismatches M9 exists to report.
func TestDocsManagedAgentConvergeHandoffsMatchTheTree(t *testing.T) {
	doc := readDoc(t, macDocHandoffs)
	root := NewRootCmd()
	for _, c := range []struct {
		command string
		path    []string
		state   string
		gate    string
	}{
		{"fugaro settings set", []string{"settings", "set"}, "landed", "internal/cli/settings_managed.go"},
		{"fugaro rigor set", []string{"rigor", "set"}, "owed", ""},
		{"fugaro accounts add", []string{"accounts", "add"}, "landed", "internal/cli/accounts_managed.go"},
		{"fugaro accounts rm", []string{"accounts", "rm"}, "landed", "internal/cli/accounts_rm.go"},
	} {
		line := macDocCommandLine(t, doc, c.command)
		if !strings.Contains(line, "**"+c.state+"**") {
			t.Errorf("%s calls `%s` %q; the tree says %q", macDocHandoffs, c.command, c.state, c.state)
		}
		// Find answers with the deepest command it matched, so a name that is
		// not a command comes back as its parent and the argument it did not
		// consume: the name is the test, not the error.
		got, _, err := root.Find(c.path)
		exists := err == nil && got.Name() == c.path[len(c.path)-1]
		if c.state == "owed" && exists {
			t.Errorf("%s says `%s` is owed and `fugaro %s` is a command: the row landed and the note has to move with it",
				macDocHandoffs, c.command, strings.Join(c.path, " "))
		}
		if c.state == "landed" && !exists {
			t.Errorf("%s says `%s` landed and `fugaro %s` is not a command: %v",
				macDocHandoffs, c.command, strings.Join(c.path, " "), err)
		}
		if c.gate != "" {
			if _, err := os.Stat("../../" + c.gate); err != nil {
				t.Errorf("%s names %s as the file that calls the gate, and it does not exist: %v", macDocHandoffs, c.gate, err)
			}
		}
	}
}

// TestDocsManagedAgentConvergeSkillParagraphIsAHandoff: the paragraph the setup
// skill owes is written in this folder rather than into `plugin/**`, which the
// 2026-10-30 rulings reserve. So the note carries it, and the file it belongs in
// keeps the sentence that makes the mark the owner's.
func TestDocsManagedAgentConvergeSkillParagraphIsAHandoff(t *testing.T) {
	doc := flattenDoc(readDoc(t, macDocHandoffs))
	for _, want := range []string{
		macDocSkill,
		// The paragraph is written for its owner to place, not placed.
		"handoff",
		// What it must say, in the words §3.2 uses: the commands the gate
		// never covers stay the owner's.
		"fugaro managed flag",
		"fugaro managed unflag",
		"never write that label yourself",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q: the skill's paragraph is a handoff and the note is where it is written down", macDocHandoffs, want)
		}
	}
	if !strings.Contains(readDoc(t, macDocSkill), "**never write that label yourself**") {
		t.Errorf("%s no longer says the label is the user's own write: the note hands the paragraph over, and the file's own sentence is what the paragraph is read against", macDocSkill)
	}
}

// TestDocsManagedAgentConvergeCensusRowMovedForItsPin: docs/rigor-modes/
// reconciliation.md's row counts the folders that ship one of these pins, and
// its own text says a folder that writes one moves the row in its own commit —
// "a reader who finds the row stale has found a merge that did not". This folder
// writes one, so the move is checked here as well as by
// TestDocsRigorModesReconciliationCountsTheConfigManagementPin.
func TestDocsManagedAgentConvergeCensusRowMovedForItsPin(t *testing.T) {
	const path = "docs/rigor-modes/reconciliation.md"
	doc := readDoc(t, path)
	row := mdRow(t, path, doc, "| Docs folders that ship their own `internal/cli/docs_*_test.go` pin |")
	pins, err := filepath.Glob("../../internal/cli/docs_*_test.go")
	if err != nil {
		t.Fatalf("the folders' pins could not be listed, so the census row cannot be checked: %v", err)
	}
	cols := strings.Split(row, "|")
	if len(cols) < 4 {
		t.Fatalf("%s' census row is not a three-column row: %q", path, row)
	}
	if stated := strings.TrimSpace(cols[2]); stated != strconv.Itoa(len(pins)) {
		t.Errorf("%s' census row states %s rows and the tree holds %d `internal/cli/docs_*_test.go` files", path, stated, len(pins))
	}
	if !strings.Contains(doc, "the managed agent converge folder's\npin, moved the table's row") {
		t.Errorf("%s does not carry the amendment sentence naming this folder's pin: the row's own text says a folder that writes one moves the row in its own commit", path)
	}
}
