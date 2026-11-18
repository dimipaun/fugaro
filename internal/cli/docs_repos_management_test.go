package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The repos-management folder's own pin (RE5). Two things a reader of the
// folder needs are held here: that its four documents are the ones the plan
// names and still say who wins where they disagree, and that the spec's own
// status line keeps the facts a later task re-reads -- the order's date, the
// decision range and the reconciliation's precedence.
//
// **What is deliberately not pinned is the opening of those status lines.** All
// four say "nothing is implemented yet" while RE1 to RE4 have merged, and the
// plan gives this task the docs it names (§ RE5's files) rather than the
// folder's own front pages: moving them is the design's own sweep, the same
// sweep that owns `docs/status.md`'s row for this spec (A8). The staleness is
// reported with this task rather than edited away here, and pinning the false
// sentence instead would hold a reader to it.
const (
	rmDocReadme  = "docs/repos-management/README.md"
	rmDocSpec    = "docs/repos-management/spec.md"
	rmDocRecon   = "docs/repos-management/reconciliation.md"
	rmDocPlan    = "docs/repos-management/implementation-plan.md"
	rmStatusFile = "docs/status.md"
)

// TestDocsReposManagementFolderCarriesItsFourDocuments: the folder is the four
// documents the plan's own table names -- the order as received, the reading of
// the code that decides what is true, the plan, and the front page that says
// which of the two wins -- and a reader who opens the front page is sent to
// each of the other three. A folder that loses one of them, or a front page
// that stops linking one, is a reader with no way to the reconciliation's
// precedence.
func TestDocsReposManagementFolderCarriesItsFourDocuments(t *testing.T) {
	for _, path := range []string{rmDocReadme, rmDocSpec, rmDocRecon, rmDocPlan} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s does not exist: %v", path, err)
		}
	}
	readme := flattenDoc(readDoc(t, rmDocReadme))
	for _, want := range []string{
		// The plan's own seven tasks and the one that is the owner's run: a
		// reader who is handed a task number reads it here first.
		"RE0–RE6",
		"RE6 stays his own run",
		// The reconciliation's precedence, which is the folder's one rule of
		// reading and the reason the front page links it first.
		"[reconciliation.md](reconciliation.md) is the one that decides what is true",
		// The four documents and their roles: the table is how a reader picks
		// which of them answers their question.
		"| [spec.md](spec.md) |",
		"| [reconciliation.md](reconciliation.md) |",
		"| [implementation-plan.md](implementation-plan.md) |",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q: the front page is what a reader of the folder opens first, and it owes the four documents and the rule that decides between them", rmDocReadme, want)
		}
	}
}

// TestDocsReposManagementSpecStatusLineKeepsItsFacts: the spec's status line is
// where a reader learns what the file is and what outranks it. The three facts
// under it -- the order's date, the decision numbers this work holds, and the
// reconciliation's precedence -- are what every later task re-reads, and a
// rewrite that drops one leaves the spec looking like the folder's authority
// rather than its record of what was asked for.
func TestDocsReposManagementSpecStatusLineKeepsItsFacts(t *testing.T) {
	line := ""
	for _, l := range strings.Split(readDoc(t, rmDocSpec), "\n") {
		if strings.HasPrefix(l, "**Status: ") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("%s carries no `**Status: ` line: the spec's own reading of itself is the first thing a reader of the folder meets", rmDocSpec)
	}
	for _, want := range []string{
		"2026-11-14",
		"`D2400`–`D2417`",
		"the reconciliation wins",
		"[implementation-plan.md](implementation-plan.md)",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("%s's status line does not say %q: %q", rmDocSpec, want, line)
		}
	}
	// The reconciliation is the other side of that precedence, and it says so
	// in its own words: the two sentences are one rule read from both files.
	recon := flattenDoc(readDoc(t, rmDocRecon))
	if !strings.Contains(recon, "this file wins") {
		t.Errorf("%s no longer says that where it and the spec disagree this file wins: the precedence the spec's status line names has to be the reconciliation's own claim too", rmDocRecon)
	}
}

// TestDocsReposManagementSpecRowIsReadable: A8's rule is that this spec's row in
// docs/status.md belongs to the design's own sweep and to whoever merges the
// spec, and that no task of RE0 to RE6 writes it. The row is therefore **read**
// and never asserted on -- what this checks is that the table still carries one,
// so a failure of the census below names a table that has this spec in it.
func TestDocsReposManagementSpecRowIsReadable(t *testing.T) {
	for _, l := range strings.Split(readDoc(t, rmStatusFile), "\n") {
		if strings.HasPrefix(l, "| repos-management |") {
			return
		}
	}
	t.Errorf("%s carries no repos-management row: A8 hands that row to the design's own sweep, and a task of this plan that wrote it would be the one thing the rule forbids", rmStatusFile)
}

// TestDocsReposManagementCensusRowMovedForItsPin: docs/rigor-modes/
// reconciliation.md's row counts the folders that ship one of these pins, and
// its own text says a folder that writes one moves the row in its own commit --
// "a reader who finds the row stale has found a merge that did not". This folder
// writes one, so the move is checked here as well as by the sibling pins that
// glob the same files.
func TestDocsReposManagementCensusRowMovedForItsPin(t *testing.T) {
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
	if !strings.Contains(doc, "the repos-management folder's\npin, moved the table's row") {
		t.Errorf("%s does not carry the amendment sentence naming this folder's pin: the row's own text says a folder that writes one moves the row in its own commit", path)
	}
}
