package orchestrator

import "testing"

// This file is compiled into this package's tests and into nothing else. It
// exposes the few values the tests pin so that a test asserts against the one
// the code uses: a number copied into a test beside the constant it is meant
// to be would go on passing after the constant moved.
const (
	MaxSummaryRunes  = maxSummaryRunes
	SummaryOpen      = summaryOpen
	SummaryClose     = summaryClose
	DelimiterRemoved = delimiterRemoved
)

// ConsumerNotCarried is the sentence the cloud consumer answers a phone with
// (P5.1f), before the acting half lands. It is exposed so a test asserts the
// reply the phone reads against the one sentence the code writes, rather than
// against a copy that would go on passing after the sentence moved.
const ConsumerNotCarried = consumerNotCarried

// MergeReadEvery is how often one repository's pull requests are read (D2742).
// It is exposed so a test advances a clock past a cadence rather than to an
// offset beside it: a cadence that moved would move the tests with it.
const MergeReadEvery = mergeReadEvery

// FoldPhase is the phase an item takes when the run behind its attempt reports
// this outcome and left, or did not leave, a pull request (G3, D2705). It is
// exposed because the mapping is the whole decision a finished run makes about
// an item, and a copy of it written into a test would go on passing after the
// mapping moved.
func FoldPhase(o Outcome, leftPR bool) (Phase, bool) { return foldPhase(o, leftPR) }

// OpPayload is the payload each op kind takes, which the patch schema's
// branches are pinned against.
func OpPayload(k OpKind) string { return opPayloads[k] }

// BookkeepingFields is the list SpecDigest skips. It is a copy, so a test that
// sorts or changes it cannot reach into the package's own slice.
func BookkeepingFields() []string { return append([]string(nil), bookkeepingFields...) }

// ResetHandoff puts the handoff seam back to the core default and clears the
// "already set" flag, before and after t: SetHandoff refuses a second call for
// the life of the process, so a test that calls it would otherwise leave the
// whole package hooked for every test after it.
func ResetHandoff(t *testing.T) {
	t.Helper()
	handoffHooks, handoffHooksSet = coreHandoffFuncs(), false
	t.Cleanup(func() { handoffHooks, handoffHooksSet = coreHandoffFuncs(), false })
}

// ResetBatch does the same for the batch seam, for the same reason.
func ResetBatch(t *testing.T) {
	t.Helper()
	batchHooks, batchHooksSet = coreBatchFuncs(), false
	t.Cleanup(func() { batchHooks, batchHooksSet = coreBatchFuncs(), false })
}

// ResetWorkspace does the same for the workspace seam, for the same reason.
func ResetWorkspace(t *testing.T) {
	t.Helper()
	workspaceHooks, workspaceHooksSet = coreWorkspaceFuncs(), false
	t.Cleanup(func() { workspaceHooks, workspaceHooksSet = coreWorkspaceFuncs(), false })
}
