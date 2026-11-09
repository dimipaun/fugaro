package runner

import (
	"context"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The security escalation (docs/security-classification/implementation-plan.md
// SC6, D1507, D1508, D1509): the run's own diff is classified against the list
// in force, and a diff that matches a row turns review level 3 on for the run.
//
// The read is (*Repo).ChangedFiles against securityBase below — roundRigor's
// own read, except that a follow-up is measured against its pull request's
// base rather than the commit it started from — and deliberately not
// runstore.Record.ChangedFiles. That field is written by
// recordChangedFiles, whose every call site is inside finalize: at the
// post-implement seam this runs at, it is still nil, so an escalation built on
// the record would classify an empty list and never fire (F5).
//
// It is called at the two seams D1507 names — the post-implement window, which
// a run that skips implement reaches without a stage of its own (a check gate
// that fixed the tree, a digest step: runner.go) and the post-fix seam beside
// roundRigor — and the escalation itself never fails the run: a diff that cannot
// be read, a resolution the forced level is refused by, and a plan that would
// not hold it are each a warning, and the levels stay where they are —
// roundRigor's own rule for the mode (F9). The three refusals after a match are
// recorded with Refused set, and finalize keeps a run carrying one out of ready
// (ReasonSecurityRefused below): the level did not run, so the review a reader
// can see does not cover the change the classification asked it to. A diff that
// cannot be read is not a match and records nothing.
//
// finalize reads the run's whole diff once more (escalateSecurityAtFinalize):
// the seams see only committed paths, and the leftover commit finalize makes is
// what turns work the agent never committed into this run's own. That reading
// can only leave a refusal — the rounds are over — and it is the one reason
// this member's `at` carries a moment no level was ever turned on at.
//
// The forced level is raise-only (D1517): a layer that wrote level 3's own
// off-switch does not defeat the escalation, which clears it and says so
// (plan.go's forcedLevelsAreRaiseOnly). Every other rule a level's settings
// have to pass still refuses — an allowed_models list that does not name the
// level's model, the same-model rule, a provider-served effort — because those
// are about *which* model the level runs and not about whether it runs.

// securityBase is the revision the escalation's read starts from: the run's
// own work (workBase) for a first run, the commit the pull request's base was
// at when the run started (follow.baseSHA) for a follow-up, so the whole pull
// request is classified and never only the commits the follow-up added to it.
//
// A follow-up resolves its levels fresh and it is this run's readiness that
// publishes the pull request's state — the branch leaves draft on it
// (runner.go's finalize). Were the read the follow-up's own commits, a pull
// request whose diff met the floor in an earlier run would be signed off by a
// follow-up that touched nothing on the list, and the level the floor asked for
// would never run over the change that asked for it (D1507, D1509) — which is
// the case that matters most: a run the floor refused leaves its pull request a
// draft, and only the next run can lift it.
//
// Both are full commit SHAs read before the agent ever ran — the commit the
// run was cut from, and the base branch bootstrap fetched — and never a ref
// name: the agent controls the checkout while a stage runs and could repoint
// origin/<base> at its own tip, which would leave this read with an empty
// range, and so the escalation with nothing to classify at all (refused.go's
// workBase, checkpoint.go's C6a).
//
// A bootstrap that could not resolve the base leaves only workBase, the
// follow-up's own commits: the escalation still classifies everything this run
// added itself, and bootstrap says what is missing.
func (r *run) securityBase() string {
	if r.follow != nil && r.follow.baseSHA != "" {
		return r.follow.baseSHA
	}
	return r.workBase()
}

// escalateSecurity classifies the run's diff and, on a match, turns review
// level 3 on and rebuilds the plan so the adversarial step is in it. The diff
// is securityBase's: the run's own work, or the whole pull request for a
// follow-up. at is the moment it is decided at (runstore.SecurityAtImplement,
// SecurityAtFix), which is what the record carries.
//
// A run that already runs level 3 escalates nothing and records nothing: it is
// already the level the escalation would reach, so nothing happened (D1509).
// The escalation is raise-only — a later moment that matches again, or that
// matches nothing because the round changed nothing, leaves the level and the
// record where they are — and level 3 is the whole of it: the rigor mode does
// not move, because a mode raise would rewrite the round counts and the
// findings policy for a change whose only new fact is its paths (D1508).
//
// In this slice the list in force is the floor alone (sensitivity.Floor); SC9
// re-points it at the one resolution, and the record member does not change
// shape when it does.
func (r *run) escalateSecurity(ctx context.Context, at string) {
	if levelOn(r.levels, policy.LevelL3) {
		return
	}
	paths, err := r.repo.ChangedFiles(ctx, r.securityBase())
	if err != nil {
		r.d.Log.Warn("reading the run's changed paths for the security escalation failed; the levels stay where they are",
			"at", at, "err", r.redact(err.Error()))
		return
	}
	matched := sensitivity.Match(paths, sensitivity.Floor())
	if len(matched) == 0 {
		return
	}
	// Read before the resolution: a level that runs has its model written into
	// the run's own configuration (setLevelModel), so the layer's own block is
	// not what a read after the resolution would see.
	turnedOff := r.levelThreeTurnedOff()
	levels, err := r.resolveLevelsForcing([]policy.Level{policy.LevelL3})
	if err != nil {
		r.d.Log.Warn("resolving the review levels with the security escalation's level 3 failed; the levels stay where they are",
			"at", at, "pattern", matched[0].Name, "err", r.redact(err.Error()))
		r.recordRefusedSecurity(at, matched[0])
		return
	}
	if !levelOn(levels, policy.LevelL3) {
		// A run that resolved no mode at all answers with no levels: there is
		// nothing to force a level into, and a plan built from them would be
		// refused below anyway.
		r.recordRefusedSecurity(at, matched[0])
		return
	}
	plan, err := r.planFor(levels)
	if err != nil {
		r.d.Log.Warn("the plan does not hold the security escalation's level 3; the levels stay where they are",
			"at", at, "pattern", matched[0].Name, "err", r.redact(err.Error()))
		r.recordRefusedSecurity(at, matched[0])
		return
	}
	r.levels, r.plan = levels, plan
	r.rec.Security = runstore.NewSecurityEscalation(at, matched[0])
	if turnedOff {
		// A layer wrote the level's own off-switch and the forced resolution
		// cleared it (plan.go, D1517). The run says so rather than overriding a
		// layer's setting in silence: the record carries that the escalation
		// happened and what matched, and this line is the only place the
		// overridden setting is named.
		r.d.Log.Warn("a layer turns review level 3 off and the run's diff matched a security pattern: the escalation's level is raise-only, so level 3 runs for this run (D1517)",
			"at", at, "pattern", matched[0].Name, "category", string(matched[0].Category))
		return
	}
	r.d.Log.Info("the run's diff matched a security pattern, so review level 3 is on for it",
		"at", at, "pattern", matched[0].Name, "category", string(matched[0].Category))
}

// escalateSecurityAtFinalize classifies the run's whole diff once finalize has
// committed the work the agent left behind, and records a refusal when that
// diff matches a row and the run never escalated: the escalation's two seams
// read the committed diff (securityBase), and a file the agent wrote without
// committing it is invisible to both of them, so the row it matches reaches the
// run for the first time here — after the last review round, when there is no
// level left to turn on (D1509).
//
// It records rather than escalates, and that is the whole of it: level 3 is the
// round that would have to run, the rounds are over, and a member claiming the
// escalation happened while no adversarial step ran would be the lie D1509's
// member exists to prevent. What it leaves keeps the run out of ready
// (applySecurityRefusal below), which is the same answer a run the seams
// refused gets.
//
// Nothing is recorded when the run already carries a member — an escalation it
// took, or a refusal at a seam — or when level 3 ran for it: the classification
// asked for a level, and the record already says what became of that.
//
// The read is the seams' own, and a fresh one rather than the set the record
// carries: runstore bounds that set (MaxChangedFiles, MaxChangedPathBytes), and
// a big enough diff would drop the very path this is here to catch. A read that
// fails records nothing, as it does at the seams.
func (r *run) escalateSecurityAtFinalize(ctx context.Context) {
	if r.rec.Security != nil || levelOn(r.levels, policy.LevelL3) {
		return
	}
	paths, err := r.repo.ChangedFiles(ctx, r.securityBase())
	if err != nil {
		r.d.Log.Warn("reading the run's whole diff for the security escalation at finalize failed; nothing is recorded for it",
			"at", runstore.SecurityAtFinalize, "err", r.redact(err.Error()))
		return
	}
	matched := sensitivity.Match(paths, sensitivity.Floor())
	if len(matched) == 0 {
		return
	}
	r.d.Log.Warn("the run's whole diff matched a security pattern and the run never escalated: the path reached the run at finalize, which commits the work the agent left uncommitted, and the review rounds are over, so the level cannot be turned on",
		"at", runstore.SecurityAtFinalize, "pattern", matched[0].Name, "category", string(matched[0].Category))
	r.recordRefusedSecurity(runstore.SecurityAtFinalize, matched[0])
}

// levelThreeTurnedOff reports whether a layer wrote level 3's own off-switch,
// which the escalation's forced resolution clears (plan.go's
// forcedLevelsAreRaiseOnly): the escalation says so rather than overriding the
// setting in silence.
func (r *run) levelThreeTurnedOff() bool {
	if levelThreeOff(r.cfg.Agent.Levels.L3) {
		return true
	}
	return r.spec.ReviewLevels != nil && levelThreeOff(r.spec.ReviewLevels.L3)
}

// recordRefusedSecurity is the record a matched row leaves when the escalation
// could not be taken: the member carries the row and Refused, so the run is not
// indistinguishable from one whose diff met nothing (D1509), and finalize reads
// it to keep the run out of ready (ReasonSecurityRefused).
//
// The first refusal is kept. A run refuses at the seam its diff first matched
// at, and a later seam that refuses again is the same answer: the record says
// where the match was found, not where it was last seen. A later seam that
// *succeeds* replaces the member outright — the escalation happened, and a
// refusal left beside it would be a reason the run no longer carries.
func (r *run) recordRefusedSecurity(at string, row sensitivity.Pattern) {
	if r.rec.Security != nil {
		return
	}
	r.rec.Security = runstore.NewRefusedSecurityEscalation(at, row)
}

// ReasonSecurityRefused is finalize's reason for a run whose diff matched a
// security row and whose escalation was refused: the level never ran, so the
// review a reader can see did not cover the change the classification asked it
// to. It is a sentence of its own rather than one of Decide's, because it is
// decided at that seam and not by the records Decide reads; the class is
// failure.Unclassified, as the owner-waiting reason's is — the run is not
// stopped for a cause the vocabulary holds, it is a draft the classification
// would not let through.
const ReasonSecurityRefused = "the run's diff matched a security pattern, but its required review level was refused"

// applySecurityRefusal is finalize's readiness rule for a refused escalation:
// the run is not ready, whatever the run's records answered, because the review
// the record shows did not cover a diff the owner's list called sensitive
// (D1509). It is a function of the record and the three answers beside it so
// that the rule is testable where finalize is not — finalize is one long
// sequence with no seam a test can call into, and this is the whole of what the
// escalation adds to it.
//
// A member that is absent or carries the escalation is passed through
// untouched: only Refused is a reason no run may be ready under.
func (r *run) applySecurityRefusal(ready bool, reason string, class failure.Class) (bool, string, failure.Class) {
	if r.rec.Security == nil || !r.rec.Security.Refused {
		return ready, reason, class
	}
	return false, ReasonSecurityRefused, failure.Unclassified
}
