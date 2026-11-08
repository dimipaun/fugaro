package cli

// The diagnose-agent plan folder, pinned.
//
// docs/diagnose-agent/ holds the owner's spec verbatim, a reconciliation with
// decisions D1600-D1614 answered (D1615-D1619 reserved and unused) and a plan
// whose Checks 410-412 are taken. One task of that plan is written and nine are
// not, and nothing in the folder is a command or a flag yet, so these tests pin
// only what rots silently: a decision row with an empty answer cell, a row in
// the reserved tail, a check number the plan no longer holds, a current status
// line that stops naming the part that is built, and a first-written status line
// dropped rather than kept.
//
// Mutation (run and restored): empty the answer cell of D1605 -> the decisions
// test fails naming D1605; add a D1615 row -> fails naming D1615; remove
// "Check 411" from the plan -> the checks test fails; take "T-DG5" out of the
// current status line -> the status test fails naming it.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	dgaDir   = "docs/diagnose-agent/"
	dgaFirst = 1600
	dgaLast  = 1614
	dgaTail  = 1619
	// dgaStatusNow marks the folder's current status line, the one a reader of
	// the directory sees first. Every folder in this repository dates a later
	// status line, so the guards below read that line rather than the kept one.
	dgaStatusNow = "**Status (2026-11-07, later):"
	// dgaStatus is that line's own first sentence as written before the first
	// task landed. It is pinned in the line kept below the current one: the
	// folder's history is kept rather than rewritten, and the claim has to stay
	// legible as what was true of the moment it describes.
	dgaStatus = "plan only; nothing is implemented; no task is approved"
)

func dgaRead(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + dgaDir + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDocsDiagnoseAgentDecisionsAreAnswered(t *testing.T) {
	row := regexp.MustCompile(`^\| \*\*D(\d+)\*\* \|`)
	seen := map[int]bool{}
	for _, line := range strings.Split(dgaRead(t, "reconciliation.md"), "\n") {
		m := row.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if n < dgaFirst || n > dgaLast {
			t.Errorf("D%d has a row but the written block is D%d-D%d (D%d-D%d are reserved and unused)", n, dgaFirst, dgaLast, dgaLast+1, dgaTail)
			continue
		}
		cells := strings.Split(strings.Trim(line, "| "), " | ")
		if len(cells) != 5 {
			t.Errorf("D%d: %d cells, want 5 (a merged row hides a decision)", n, len(cells))
			continue
		}
		if a := strings.TrimSpace(cells[4]); a == "" || strings.Contains(a, "(empty)") {
			t.Errorf("D%d has no answer", n)
		}
		seen[n] = true
	}
	for n := dgaFirst; n <= dgaLast; n++ {
		if !seen[n] {
			t.Errorf("D%d has no row", n)
		}
	}
	if !strings.Contains(dgaRead(t, "reconciliation.md"), "**Not the recommendation as first answered") {
		t.Error("D1600 and D1603 must stay recorded as the owner's answers that were not the recommendation")
	}
}

// dgaStatusLine is f's current status line: the one opening with dgaStatusNow.
func dgaStatusLine(t *testing.T, f string) string {
	t.Helper()
	for _, line := range strings.Split(dgaRead(t, f), "\n") {
		if strings.HasPrefix(line, dgaStatusNow) {
			return line
		}
	}
	t.Fatalf("%s%s carries no line opening with %q: T-DG5's two files are in the tree, and the folder's status has to say so -- if the line was reworded, move this guard's marker with it", dgaDir, f, dgaStatusNow)
	return ""
}

// TestDocsDiagnoseAgentStatusAndChecks: the folder states its own state in one
// current line per document, and that line has to move with the tree. T-DG5's
// files landed while both said nothing was implemented, so the current line
// names the one task that is written and the first-written line is kept below
// it, still reading as what was true then. The clauses checked here are the two
// a reader decides on: what is built, and that the rest of the plan is not
// approved.
func TestDocsDiagnoseAgentStatusAndChecks(t *testing.T) {
	for _, f := range []string{"README.md", "implementation-plan.md"} {
		line := dgaStatusLine(t, f)
		for _, want := range []string{"T-DG5", "no other task is approved"} {
			if !strings.Contains(line, want) {
				t.Errorf("%s%s: the current status line does not say %q, so the folder reads as if T-DG5's files were not in the tree, or as if the nine tasks after it were approved: %s", dgaDir, f, want, line)
			}
		}
		if strings.Contains(line, "nothing is implemented") {
			t.Errorf("%s%s: the current status line still says nothing is implemented, though internal/diagnoseagent/creds.go and creds_test.go are in the tree: %s", dgaDir, f, line)
		}
		if !strings.Contains(dgaRead(t, f), dgaStatus) {
			t.Errorf("%s%s: the status as first written, %q, is gone: the folder keeps the line it was written with below the current one, so the claim stays readable as what was true of that moment", dgaDir, f, dgaStatus)
		}
	}
	plan := dgaRead(t, "implementation-plan.md")
	for _, c := range []string{"Check 410", "Check 411", "Check 412"} {
		if !strings.Contains(plan, c) {
			t.Errorf("the plan no longer holds %s", c)
		}
	}
}
