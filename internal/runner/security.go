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
// The read is roundRigor's own — (*Repo).ChangedFiles(ctx, r.workBase()) — and
// deliberately not runstore.Record.ChangedFiles. That field is written by
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
// The forced level is raise-only (D1517): a layer that wrote level 3's own
// off-switch does not defeat the escalation, which clears it and says so
// (plan.go's forcedLevelsAreRaiseOnly). Every other rule a level's settings
// have to pass still refuses — an allowed_models list that does not name the
// level's model, the same-model rule, a provider-served effort — because those
// are about *which* model the level runs and not about whether it runs.

// escalateSecurity classifies the run's diff and, on a match, turns review
// level 3 on and rebuilds the plan so the adversarial step is in it. at is the
// moment it is decided at (runstore.SecurityAtImplement, SecurityAtFix), which
// is what the record carries.
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
	paths, err := r.repo.ChangedFiles(ctx, r.workBase())
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
