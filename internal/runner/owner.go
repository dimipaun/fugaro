package runner

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// OD1: a finding whose only remedies are a decision that is not the run's to
// make stops the loop instead of buying fix rounds. The reviewer marks it
// (Finding.NeedsOwner), the loops never hand it to a fixer (fixableFindings)
// and a round that leaves nothing else ends the run waiting for the owner
// (ownerStop). The owner's answer comes back as a follow-up launched with
// `fugaro run --pr N --owner-ruled` (internal/cli's refusal names the flag).

// ownerWaitingReason is why a run that stopped on the owner's decisions is
// not ready: the count, in the words the record's reason, the pull request's
// status block, `fugaro runs ls` and `fugaro diagnose` all print.
func ownerWaitingReason(n int) string {
	return fmt.Sprintf("review is waiting on %s", Plural(n, "owner decision"))
}

// pendingOwner is how many decisions a record says the run is waiting on, 0
// for a record carrying no block.
func pendingOwner(od *runstore.OwnerDecisions) int {
	if od == nil {
		return 0
	}
	return od.Total
}

// ownerStop records the decisions a round left and halts the run: the round
// asked for changes and every finding in it is one only the owner can settle,
// so no fix round can change the answer and no further round is worth buying.
// The pull request stays a draft — the halt is not a limit the run passed but
// a question it cannot answer — and finalize writes the block into the record
// and the decisions into the status section.
//
// It reports whether the halt was recorded, which is false when a halt or a
// cancel came first (a budget cap, a kill switch): that stop is the run's, and
// the decisions are not recorded over it.
func (r *run) ownerStop(ctx context.Context, findings []Finding) bool {
	ds := make([]runstore.OwnerDecision, 0, len(findings))
	for _, f := range findings {
		ds = append(ds, runstore.OwnerDecision{File: r.redact(f.File), Summary: r.redact(f.Summary)})
	}
	blk := runstore.NewOwnerDecisions(ds)
	if blk == nil {
		// A needs_owner finding with no summary at all: nothing a person could
		// answer, so nothing is recorded and the round is read as the round it
		// otherwise is. It cannot happen through the stage's own schema, whose
		// findings require a summary; a hand-built verdict is the case.
		return false
	}
	r.mu.Lock()
	r.rec.OwnerDecisions = blk
	r.mu.Unlock()
	r.save(ctx)
	if !r.haltNow(runstore.Halt{Reason: runstore.HaltOwnerDecision, Scope: "run", At: r.d.Now().UTC(),
		Detail: ownerWaitingReason(blk.Total)}) {
		return false
	}
	r.d.Log.Warn("the review left findings only the owner can settle; the run stops waiting for them",
		"decisions", blk.Total)
	return true
}
