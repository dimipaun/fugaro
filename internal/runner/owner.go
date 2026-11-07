package runner

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/followup"
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

// workingRuling is the ruling this run works from and the decisions it
// answers (OD1): the one its own launch was given (task.Spec.OwnerRuled) and
// the block the run before it left waiting, which that ruling was accepted
// against — or, when the launch carries none, the pair the run before it
// worked from, which this follow-up inherits.
//
// The inheritance is what keeps a settled question settled. A ruled run whose
// review accepts the ruling leaves no decisions of its own, so nothing on its
// record says the owner answered anything; the next follow-up's reviewers,
// handed no ruling, would read the same diff, mark the same finding
// needs_owner again and halt the run — which is the loop the ruling exists to
// end, one run further out. The pair is read from the run before it
// (prepareFollowUp), so the ruling goes on from follow-up to follow-up with
// the questions it answers until a newer ruling supersedes it: a run launched
// with a ruling of its own answers the block its own previous run left, and
// that is the pair it records and passes on.
//
// A launch that carries no ruling and follows a run that worked from none
// gives "", nil, which builds no addendum: a ruling nothing answers is the
// launch's text settling whatever it names, and no reviewer is handed it as an
// answer (followup.RulingAddendum, and the launcher's refusal of a ruling with
// nothing to answer).
func (r *run) workingRuling() (string, *runstore.OwnerDecisions) {
	if r.spec.OwnerRuled != "" {
		if r.follow == nil {
			return r.spec.OwnerRuled, nil
		}
		return r.spec.OwnerRuled, r.follow.prevOwner
	}
	if r.follow == nil || r.follow.prevRuledDecisions == nil {
		return "", nil
	}
	return r.follow.prevRuled, r.follow.prevRuledDecisions
}

// recordRuling writes the ruling this run works from onto the record with the
// decisions it answers (OD1), which is the pair a later follow-up inherits.
// The bootstrap writes the launch's own text onto the record with the rest of
// what the task says the run is; this refines it once the run before this one
// has been read, which is the only thing that can say what the ruling answered
// and whether there is one to inherit at all.
func (r *run) recordRuling() {
	if text, od := r.workingRuling(); text != "" {
		r.rec.OwnerRuled, r.rec.OwnerRuledDecisions = text, od
	}
}

// ownerRuling is workingRuling as the brief builders take it: the ruling and
// the decisions it answers, or the zero value, which builds no addendum.
func (r *run) ownerRuling() followup.OwnerRuling {
	text, od := r.workingRuling()
	out := followup.OwnerRuling{Text: text}
	if od == nil {
		return out
	}
	out.Total = od.Total
	for _, d := range od.Decisions {
		out.Decisions = append(out.Decisions, followup.OwnerDecision{File: d.File, Summary: d.Summary})
	}
	return out
}

// decidingOwnerDecisions is what the run's review left the repository's owner:
// the needs_owner findings of the last round of every enabled outer review
// level, redacted and bounded as the record stores them, nil when they left
// none.
//
// It reads every enabled outer level and not only the deciding one (the last
// round of the highest level that ran, decidingReview) because both are levels
// the pull request's readiness is read from: level 3 overwrites the senior
// level's round in lastSenior, so a senior round that shipped while carrying a
// decision — or that ran out of rounds with one beside a fixable finding, which
// leaves the pull request a draft because that level did not ship — would
// otherwise record the question nowhere: nothing would list it for the owner
// and the follow-up that answers it would be launched blind over it, which is
// the loop this whole change exists to end. The rounds before a level's last
// one are not read: a later round of the same level superseded them.
//
// A first line under an outer level advises that level and never decides
// (seniorReview), so its rounds are not read here; where no outer level ran it
// is the deciding level, and lastSenior holds its round.
//
// The verdict beside the findings is not read: a round that shipped while
// naming one of these still leaves the question open, and Decide's gate is what
// keeps the pull request a draft for it.
func (r *run) decidingOwnerDecisions() *runstore.OwnerDecisions {
	var ds []runstore.OwnerDecision
	seen := make(map[runstore.OwnerDecision]bool)
	// Senior before the deciding round, which is the adversarial level's where
	// it ran: a decision both levels left is one entry, listed under the level
	// that raised it first.
	for _, round := range []seniorRound{r.seniorLevelLast, r.lastSenior} {
		for _, f := range ownerFindings(round.Findings) {
			d := runstore.OwnerDecision{File: r.redact(f.File), Summary: r.redact(f.Summary)}
			if seen[d] {
				continue
			}
			seen[d] = true
			ds = append(ds, d)
		}
	}
	if len(ds) == 0 {
		return nil
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
