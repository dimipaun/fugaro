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
