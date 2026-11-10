package cli

// Launching a run for a repository without its checkout (docs/run-without-checkout/):
// the folder in which the owner answered that a launch outside a checkout reads
// the ref's `fugaro.yaml` through the installation's credentials service, as the
// operator themselves (D902, 2026-11-04), and the plan (A0–A4, B1–B2, C1, D1)
// that builds it.
//
// **A0 and A1 have landed**, and they are the tasks of the plan that are in the
// tree: `internal/gitprov/github` carries `personReadTokenPermissions` and its
// exported accessor, and the credentials service's GitHub read mint asks with it,
// so the token the route hands out carries `contents: read` beside the
// coordinator's two and the file read this design is for is served where the
// project publishes a service; and `github.FileAt` reads one file at a ref through
// a `gitprov.TokenSource` its caller supplies, which is what A3's launcher seam is
// built on. The folder's four documents are the record of the ask and of each
// answer, and every sentence in them that says what the tree does is a claim
// that went stale the day a task landed: a status line still reading "nothing
// implemented", a spec section saying the read is refused until A0 lands, a
// reconciliation finding saying the mint's set does not cover a file read, and a
// D915 row still marked unanswered. Those are what this file checks — against the
// tree, not against the brief — and what it leaves alone is the ask itself:
// spec.md is the design's own account of the behaviour and reconciliation.md keeps
// each decision with the date and the reason the owner answered it, so the answers
// stay as written even where the wording around them moves.

import (
	"strings"
	"testing"
)

const rwcDir = "docs/run-without-checkout/"

// rwcDocs are the four documents the folder ships, in the order README.md lists
// them. The list is written here rather than derived from the directory so that a
// document that disappears — or a fifth that arrives without the index naming it —
// fails instead of quietly shrinking what these checks read.
var rwcDocs = []string{"README.md", "spec.md", "reconciliation.md", "implementation-plan.md"}

// rwcLandedAccessor is the code the folder's status lines are read against: the
// accessor A0 added beside the five permission sets, which is what makes every
// pre-A0 sentence in the folder false. It is searched for as a declaration rather
// than by a file's existence, because the file it lives in predates A0.
const rwcLandedAccessor = "func PersonReadTokenPermissions() map[string]string"

// rwcStatusParagraph is the paragraph a document opens with about the tree — the
// one starting **Status:** — flattened so a phrase spanning its wraps is visible.
// It is the folder's claim about itself rather than about the ask, and it is the
// paragraph every task's landing moves.
func rwcStatusParagraph(t *testing.T, path string) string {
	t.Helper()
	for _, para := range strings.Split(readDoc(t, path), "\n\n") {
		if strings.HasPrefix(strings.TrimSpace(para), "**Status:") {
			return flattenDoc(para)
		}
	}
	t.Fatalf("%s carries no paragraph opening with %q: it is the folder's claim about the tree, and the one paragraph a reader checks against it", path, "**Status:")
	return ""
}

// rwcRetired are the spellings the folder was written with before A0 landed and
// which are false now. Each is a claim about the tree rather than about the ask:
// while A0 is in it, a document carrying one tells a reader that the file read
// this design is for is refused, which is the state A0 removed. The records of
// what was read at the time are not in this list — reconciliation.md's findings
// keep their reading and carry an amendment beside it, which is what a
// reconciliation is for — and neither is A0's own red step, which describes the
// `main` the task was written against.
var rwcRetired = []string{
	"nothing implemented",
	"no task approved",
	"cannot read a file today",
	"the set the read needs is not the one the route mints with",
	"the read is refused until A0 lands",
	"mints with `github.CoordinatorTokenPermissions()`",
}

// TestDocsRunWithoutCheckoutStatesA0AsLanded: while A0's accessor is in the tree,
// every document in the folder says so and none of them still describes the read
// as refused. A0 is the plan's first task and the one whose absence made the whole
// design unbuildable, so a folder that has not moved with it is a folder whose
// first paragraph contradicts the code it cites.
func TestDocsRunWithoutCheckoutStatesA0AsLanded(t *testing.T) {
	if !strings.Contains(readDoc(t, "internal/gitprov/github/apptoken.go"), rwcLandedAccessor) {
		t.Skipf("A0's accessor is not in the tree: the folder's pre-A0 sentences are the true ones, and these checks read the tree")
	}
	for _, name := range rwcDocs {
		path := rwcDir + name
		status := rwcStatusParagraph(t, path)
		if !strings.Contains(status, "A0") || !strings.Contains(status, "landed") {
			t.Errorf("%s's status line does not say that A0 landed: A0's set is in the tree, so the line is a claim about the tree and the task that made the read possible is the one it has to name", path)
		}
		for _, stale := range []string{"nothing implemented", "no task approved"} {
			if strings.Contains(status, stale) {
				t.Errorf("%s's status line still says %q while A0 is in the tree: that is a folder contradicting the code it cites", path, stale)
			}
		}
	}
	for _, name := range rwcDocs {
		doc := flattenDoc(readDoc(t, rwcDir+name))
		for _, stale := range rwcRetired {
			if strings.Contains(doc, stale) {
				t.Errorf("%s still says %q: A0 has landed, so the read this design is for is served by the token the route mints, and a reader told otherwise is told the first task of the plan is still owed", rwcDir+name, stale)
			}
		}
	}
	// The other half, and the one that keeps the list above honest: the folder
	// names the set that landed. It is the accessor A0 added, and a document that
	// described the read without it would be describing a token without saying
	// what it reaches.
	for _, name := range []string{"README.md", "spec.md", "implementation-plan.md"} {
		if !strings.Contains(flattenDoc(readDoc(t, rwcDir+name)), "PersonReadTokenPermissions") {
			t.Errorf("%s never names `github.PersonReadTokenPermissions`, the set A0 landed and the one this read's token carries: the folder would describe a read without the permission that makes it possible", rwcDir+name)
		}
	}
}

// TestDocsRunWithoutCheckoutRecordsD915Answered: D915 — the question the code
// itself raised, whether the person read mint gets a set of its own or a route of
// its own — is answered, and the folder records the answer rather than leaving the
// row open. README.md said "every decision is answered but one" while that was the
// one, and the reconciliation's §3 table is where the answer belongs.
func TestDocsRunWithoutCheckoutRecordsD915Answered(t *testing.T) {
	recon := flattenDoc(readDoc(t, rwcDir+"reconciliation.md"))
	i := strings.Index(recon, "| **D915** |")
	if i < 0 {
		t.Fatalf("%s no longer carries a §3 row for D915: it is the decision A0 implements, and this check reads it", rwcDir+"reconciliation.md")
	}
	row := recon[i:]
	// D915 is the last row of §3, so the row runs to the section's end: the
	// next row's marker is the first bound and the next heading the second.
	if j := strings.Index(row[1:], "| **D9"); j >= 0 {
		row = row[:j+1]
	}
	if j := strings.Index(row, " ## "); j >= 0 {
		row = row[:j]
	}
	if strings.Contains(row, "no answer") || strings.Contains(row, "Open") {
		t.Errorf("%s's D915 row is still recorded as unanswered: the set of its own landed with A0, and the answer this row carries is the one the plan was written against", rwcDir+"reconciliation.md")
	}
	for _, want := range []string{"A set of its own", "landed"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's D915 row does not carry %q: the row is where the answer and its landing are read, and a row that dropped either leaves a reader unsure whether the question was taken", rwcDir+"reconciliation.md", want)
		}
	}
	readme := flattenDoc(readDoc(t, rwcDir+"README.md"))
	if strings.Contains(readme, "Every decision is answered but one") {
		t.Errorf("%s still says every decision is answered but one: D915 is the one that was outstanding, and it is answered and landed", rwcDir+"README.md")
	}
}
