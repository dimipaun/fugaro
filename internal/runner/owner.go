package runner

import (
	"context"
	"fmt"
	"strings"

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

// workingRulings is the rulings this run works from, newest first, each with
// the decisions it answered (OD1): the one its own launch was given
// (task.Spec.OwnerRuled), accepted against the block the run before it left
// waiting — or, when the launch carries none, the rulings the run before it
// worked from, which this follow-up inherits — and, behind the newest pair,
// the rulings those runs worked from, each with the decisions it settled.
//
// The inheritance is what keeps a settled question settled. A ruled run whose
// review accepts the ruling leaves no decisions of its own, so nothing on its
// record says the owner answered anything; the next follow-up's reviewers,
// handed no ruling, would read the same diff, mark the same finding
// needs_owner again and halt the run — which is the loop the ruling exists to
// end, one run further out. The pairs are read from the run before it
// (prepareFollowUp), so the rulings go on from follow-up to follow-up with the
// questions they answer until a newer ruling supersedes them.
//
// The whole chain and not only the newest pair, because a ruling answers the
// decisions of the run it was launched over and no others: the owner answers
// one question, the review raises another, and a follow-up carrying the new
// ruling alone leaves the first question open — its reviewers, told nothing
// about it, mark it needs_owner again, the run stops on it, and the ruling
// after that drops the second one in turn. Two questions, two rulings, a run
// per swap and no end to it. Carrying every pair the runs before it held is
// what makes an answer outlive the ruling that gave it
// (runstore.Record.OwnerRuledEarlier, followup.RulingAddendum).
//
// A launch that carries no ruling and follows a run that worked from none
// gives nothing, which builds no addendum: a ruling nothing answers is the
// launch's text settling whatever it names, and no reviewer is handed it as an
// answer (followup.RulingAddendum, and the launcher's refusal of a ruling with
// nothing to answer).
func (r *run) workingRulings() []runstore.OwnerRuling {
	if r.follow == nil {
		if r.spec.OwnerRuled == "" {
			return nil
		}
		// A first run answers nothing: the launcher accepts --owner-ruled only
		// where the run before it left decisions to answer (internal/cli,
		// resolvePR), and a first run has no run before it.
		return []runstore.OwnerRuling{{Ruling: r.spec.OwnerRuled}}
	}
	if r.spec.OwnerRuled == "" {
		return r.follow.prevRulings
	}
	return append([]runstore.OwnerRuling{{Ruling: r.spec.OwnerRuled, Decisions: r.follow.prevOwner}},
		r.follow.prevRulings...)
}

// rulingText is the newest ruling, the one the run's implement brief works
// from: "" when the run works from none.
func (r *run) rulingText() string {
	if rs := r.workingRulings(); len(rs) > 0 {
		return rs[0].Ruling
	}
	return ""
}

// recordRuling writes the rulings this run works from onto the record, each
// with the decisions it answered (OD1): the newest pair in OwnerRuled and
// OwnerRuledDecisions, the pairs behind it in OwnerRuledEarlier. That is what
// a later follow-up inherits. The bootstrap writes the launch's own text onto
// the record with the rest of what the task says the run is; this refines it
// once the run before this one has been read, which is the only thing that can
// say what each ruling answered and what there is to inherit at all.
func (r *run) recordRuling() {
	rs := r.workingRulings()
	if len(rs) == 0 {
		return
	}
	// The newest pair is recorded as the launch gave it, with no decisions
	// beside it when it answered none this record can name: the ruling is the
	// text the run works from, and the brief states it whether or not there is
	// a list to show under it.
	r.rec.OwnerRuled = rs[0].Ruling
	r.rec.OwnerRuledDecisions = rs[0].Decisions.Bounded()
	if earlier := runstore.BoundedRulings(rs[1:]); len(earlier) > 0 {
		r.rec.OwnerRuledEarlier = earlier
	}
}

// ownerRulings is workingRulings as the brief builders take it: the rulings
// with the decisions each answers, newest first, or nothing, which builds no
// addendum.
func (r *run) ownerRulings() []followup.OwnerRuling {
	rs := r.workingRulings()
	out := make([]followup.OwnerRuling, 0, len(rs))
	for _, e := range rs {
		o := followup.OwnerRuling{Text: e.Ruling}
		if d := e.Decisions; d != nil {
			o.Total = d.Total
			for _, dec := range d.Decisions {
				o.Decisions = append(o.Decisions, followup.OwnerDecision{File: dec.File, Summary: dec.Summary})
			}
		}
		out = append(out, o)
	}
	return out
}

// ownerNoSummary is the sentence a decision is stored with when the review
// left the finding to the owner and no words for it.
//
// VerdictSchema asks the model for a summary of at least one character, but it
// constrains a model as it decodes and the runner does not enforce it on a
// verdict it reads (decodeVerdict): a finding can still reach the loop marked
// needs_owner with the key present and nothing in it, and
// runstore.NewOwnerDecisions stores no decision without one. The block would
// then be dropped whole, which is not "a decision nobody could answer" but
// "no decision at all": the run would end the draft any clean run ends
// instead of the one waiting on the owner, Decide's gate would never fire,
// the status section would carry the generic draft note instead of "Waiting
// on you", and the launcher would let the next blind follow-up through — the
// loop this whole change exists to end, started by a sentence nobody wrote.
// The sentence says only what happened, so a reader is told the question is
// there without words being put in the reviewer's mouth.
const ownerNoSummary = "the reviewer left this finding to the owner with no summary of it"

// ownerDecision is one needs_owner finding as the record stores it: the path
// the reviewer named and their sentence for it, both redacted, or
// ownerNoSummary where they left none (above).
func (r *run) ownerDecision(f Finding) runstore.OwnerDecision {
	summary := r.redact(f.Summary)
	if strings.TrimSpace(summary) == "" {
		summary = ownerNoSummary
	}
	return runstore.OwnerDecision{File: r.redact(f.File), Summary: summary}
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
			d := r.ownerDecision(f)
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
		// The review left nothing for the owner: no block, no halt, and the
		// round is read as the round it otherwise is. A finding that is one of
		// these always reaches the record with a sentence (ownerDecision), so
		// nil here is an empty list, never a list whose sentences were
		// unreadable.
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
		ds = append(ds, r.ownerDecision(f))
	}
	blk := runstore.NewOwnerDecisions(ds)
	if blk == nil {
		// Unreachable through the round that calls this, which passed at least
		// one finding and every one of them is stored with a sentence
		// (ownerDecision); it is the answer for a caller that passed none at
		// all: nothing a person could answer, so nothing is recorded and no
		// halt is taken.
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
