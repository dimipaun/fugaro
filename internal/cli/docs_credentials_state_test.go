package cli

import (
	"strings"
	"testing"
)

// What this test holds, in docs/credentials-service/implementation-plan.md:
//
//   - C1.10 states the requirement the independent re-review of C1.9 (finding
//     N4) found missing: the two nodes C1.9 writes per run — `creds/current/*`
//     and `creds/leases/*` — are deleted when the run ends, and C1.9 is not the
//     task that deletes them. Nothing in C1.9 deletes a `creds/current` node at
//     all, and its lease sweep prunes only lapsed leases above the residue, so
//     without this requirement recorded the plan grew one node per run a project
//     has ever had with no task owning the cleanup.
//   - C1.11 records the observation that came with it and that must NOT be
//     fixed in code: under `fallback: auto`, D154's stickiness keeps a run on
//     the paid key for the rest of the run.
//
// It is deliberately about wording and not about behaviour: nothing is
// implemented by the document it reads. Its red step was run for real — both
// halves fail against the plan before the two notes are in it, and against a
// copy of the amended plan with either note removed.
func TestDocsCredentialsServiceStatesTheRunNodeCleanupAndThePaidKeyStickiness(t *testing.T) {
	const plan = "docs/credentials-service/implementation-plan.md"
	// The phrases are matched across the line breaks the plan's prose wraps at
	// (flattenDoc), so a re-wrapped sentence is not read as a lost claim.
	doc := flattenDoc(readDoc(t, plan))

	c110 := taskSection(t, plan, doc, "### Task C1.10:")
	for _, want := range []string{
		"creds/current/<slug>/<run>",
		"creds/leases/<slug>/<run>",
		"deleted when the run ends",
		"nothing in C1.9 ever deletes the first of them",
		"the write-once outcome is the liveness signal",
		"is this task's to choose",
		"carries a credential value",
	} {
		if !strings.Contains(c110, want) {
			t.Errorf("%s's C1.10 no longer states %q: the run's creds/ nodes are a requirement no other task in this plan carries, so dropping the note leaves the cleanup unowned", plan, want)
		}
	}

	c111 := taskSection(t, plan, doc, "### Task C1.11:")
	for _, want := range []string{
		"stickiness keeps a run on the paid key for the rest of the run",
		"the calls after it take the paid key again",
		"the prompt cache's",
		`"this call may spend once"`,
		"for as long as they are on the paid key",
		"No code change follows from it",
	} {
		if !strings.Contains(c111, want) {
			t.Errorf("%s's C1.11 no longer records %q: the paid-key stickiness is a consequence of D142, D152 and D154 that an operator has to be able to read before writing `fallback: auto`", plan, want)
		}
	}
	// The cleanup's home in the call path, and the fact that it is required
	// rather than merely possible.
	if !strings.Contains(c111, "creds/") || !strings.Contains(c111, "finding N4") {
		t.Errorf("%s's C1.11 no longer points at the cleanup requirement C1.10 states (finding N4): the requirement has to have a home in the call path", plan)
	}
}

// taskSection is one task of the plan: from its heading to the next task
// heading, so a phrase asserted here is that task's and not another's. The
// phrases in this test appear once in the document, and slicing keeps it that
// way if a later task quotes them.
func taskSection(t *testing.T, path, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("%s has no %q: the task this test reads is gone", path, heading)
	}
	rest := doc[start+len(heading):]
	if end := strings.Index(rest, "\n### "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestDocsCredentialsServiceDoesNotClaimTheStateStaysOutOfTheRunBinary pins
// C1.9's **Interfaces.** sentence against the claim the boundary test this
// branch adds says is false (the review's finding).
//
// The plan said "nothing here is ever linked into the runner". The true claim —
// the one `internal/creds/boundary_test.go` pins, and says in its own doc that
// it is not the old one — is narrower: no *run-side package imports* the state.
// cmd/fugaro links internal/cli and the runner into ONE binary, so the binary a
// run's image carries does contain internal/cli and, from C1.21, the state above
// its account commands. The word "ever" made the plan contradict its own later
// task (C1.21 puts the package in internal/cli) on the page whose whole point is
// that the plan and the code agree, and a reader could take it as "the process
// cannot hold this code", which is not what keeps a run out of creds/ — the
// rules are (O1, C1.3).
//
// The red step was run for real: this test fails against the plan with the old
// sentence in it, and passes once it says what the boundary test pins.
func TestDocsCredentialsServiceDoesNotClaimTheStateStaysOutOfTheRunBinary(t *testing.T) {
	const plan = "docs/credentials-service/implementation-plan.md"
	doc := taskSectionOf(t, plan, "### Task C1.9:")
	for _, want := range []string{
		"no run-side package imports it",
		"credsImporters",
		// The program name is backticked in the plan, so it is not part of the
		// phrase this looks for.
		"links the CLI and the runner into one binary",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s's C1.9 no longer says %q: the Interfaces sentence has to state the claim the boundary test pins, which is an import boundary and not a claim about what the run's binary contains", plan, want)
		}
	}
	if strings.Contains(doc, "ever linked into the runner") {
		t.Errorf("%s's C1.9 says the state is \"ever linked into the runner\" again: cmd/fugaro links internal/cli and the runner into one binary, so that is false, and it is not what keeps a run out of creds/ — the rules are (O1, C1.3)", plan)
	}
}

// taskSectionOf is taskSection from the flattened document, for a caller that
// has no need of the unflattened text.
func taskSectionOf(t *testing.T, path, heading string) string {
	t.Helper()
	return taskSection(t, path, flattenDoc(readDoc(t, path)), heading)
}
