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
// (ownerStop); one the deciding round leaves beside a fixable finding, or
// carries while shipping, ends the run a draft that waits on it all the same
// (recordOwnerDecisions, Decide's gate). The owner's answer comes back as a
// follow-up launched with `fugaro run --pr N --owner-ruled` (internal/cli's
// refusal names the flag).

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

// decidingOwnerDecisions is what the run's review left the repository's owner:
// the deciding round's needs_owner findings, redacted and bounded as the
// record stores them, nil when that round left none.
//
// It reads the deciding round — the last round of the level readiness is
// decided on (decidingReview) — and not every round the run held, because that
// is the round the pull request's outcome is read from: a round a later one
// superseded is not what the pull request is waiting for, and a first line
// under an outer level advises that level and never decides (seniorReview).
// The verdict beside the findings is not read: a round that shipped while
// naming one of these still leaves the question open, and Decide's gate is
// what keeps the pull request a draft for it.
func (r *run) decidingOwnerDecisions() *runstore.OwnerDecisions {
	findings := ownerFindings(r.lastSenior.Findings)
	if len(findings) == 0 {
		return nil
	}
	ds := make([]runstore.OwnerDecision, 0, len(findings))
	for _, f := range findings {
		ds = append(ds, runstore.OwnerDecision{File: r.redact(f.File), Summary: r.redact(f.Summary)})
	}
	return runstore.NewOwnerDecisions(ds)
}

// recordOwnerDecisions writes the decisions the deciding round left into the
// record, and reports whether it wrote a block. Finalize calls it before
// Decide reads the record, so a run whose review left a question the owner has
// to answer ends a draft that carries it — whatever the round's verdict was,
// and whether or not the loop stopped on it (ownerStop). The status section
// (report.go) and `fugaro runs ls` print it from there, and the launcher
// refuses a follow-up of it without the owner's ruling (internal/cli).
//
// A run that stopped for its own reason records nothing: a cap the run
// crossed, a kill switch or a cancel is why it ended, and the decisions are
// not what it is waiting for. That is the same rule ownerStop follows by
// writing only after its halt is the one that landed.
func (r *run) recordOwnerDecisions(halt *runstore.Halt) bool {
	if r.rec.OwnerDecisions != nil || halt != nil || r.isCancelled() {
		return false
	}
	blk := r.decidingOwnerDecisions()
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
	return true
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
// the decisions are not recorded over it. The halt is decided first and the
// block written only once it landed, so the record cannot say the run is
// waiting on the owner and on the cap at the same time.
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
	if !r.haltNow(runstore.Halt{Reason: runstore.HaltOwnerDecision, Scope: "run", At: r.d.Now().UTC(),
		Detail: ownerWaitingReason(blk.Total)}) {
		return false
	}
	r.mu.Lock()
	r.rec.OwnerDecisions = blk
	r.mu.Unlock()
	r.save(ctx)
	r.d.Log.Warn("the review left findings only the owner can settle; the run stops waiting for them",
		"decisions", blk.Total)
	return true
}
