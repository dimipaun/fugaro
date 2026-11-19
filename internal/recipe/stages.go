package recipe

import "slices"

// StageBound is the most stages a run of r can start with first-line rounds f
// and review rounds n (the knobs), implement included. Check steps are
// runner commands with their own timeout, so they are not model stages and
// do not count.
//
// ModeReview and ModeArtifact have neither loop: a mode: review recipe is one
// senior review, and a mode: artifact recipe may begin with one plan stage
// whose only job is to write a render plan (design artifact-runs.md §3, §6).
// Its render step is a runner command with its own timeout -- the same rule
// that keeps a check step out -- so one is the bound, and for a
// render-only recipe (steps: [render]) the bound errs high rather than low,
// which is the direction a ceiling may err in. A diagnose step is a model
// stage where a render is not, so the artifact shape holding one beside a plan
// counts the two (see the arm below).
//
// A digest recipe counts no implement stage either: its first step is the
// runner's own work, which writes the run reports with no model call
// (run-digest), and a commit-reports recipe's first step is the runner's own
// the same way (commit-reports, E5) -- so the review loop after either is all
// a run of it can record.
// The loop's fix stays counted: whether the review of a report ever asks for
// one is for that run to decide, and a bound errs high rather than low.
//
// A review step with hand_over buys no fix round, so each of its rounds is
// the review alone (per below): the run ends at that review with its verdict
// recorded, and the escalation's `escalate` recipe is one pass and one
// verdict by construction. A recipe holding the rebase step is a single model
// stage at most, whatever n says, because the step is the whole run: the
// conflicts are the only work a model is asked about (see the arm below).
//
// An adversarial step (review level 3) adds n3*(2 + 2*f) when it bounces and
// n3*2 when it does not: each of its n3 rounds is one adversarial review and
// its fix, and a bounce inserts the same cheap first-line loop a bouncing
// review round does. n3 is the step's own max_rounds, or n3Default where it
// writes none. That default is the caller's to pass, because a rigor mode
// lives in internal/rigor and that package's own test imports this one, so the
// import cannot go the other way: a caller that resolved a mode passes
// rigor.AdversarialDefault(mode) — the mode's senior row, the number the
// senior review starts from — and a caller that has no mode passes the
// strictest row the modes' table holds, rigor.AdversarialDefault(rigor.Critical),
// 5. Both are one source, and the direction a bound may err in is high: a
// recipe that wants the shorter term writes the step's own max_rounds. The
// step's own bound errs high the same way: a round that ends the run early
// costs fewer stages, and a recipe that writes no bounce still pays only n3*2.
func StageBound(r *Recipe, f, n, n3Default int) int {
	if r.Mode == ModeReview {
		return 1
	}
	if r.Mode == ModeArtifact {
		// A plan stage and a diagnosis are model stages and a render is not.
		// The one artifact shape that holds both of the first two ([plan,
		// diagnose], recipe.Parse) starts two, and the bound counts the
		// stages that are really there rather than the mode's one: a bound
		// errs high rather than low, and this is the direction it would err
		// in for the shape the diagnose step adds.
		stages := 0
		for _, s := range r.Steps {
			if s.Kind == StepPlan || s.Kind == StepDiagnose {
				stages++
			}
		}
		if stages > 1 {
			return stages
		}
		return 1
	}
	// A rebase recipe is one model stage at most: the conflicts are the only
	// thing in it a model is asked about, and a rebase that resolves them
	// without one -- or refuses before the stage at all -- costs fewer
	// stages still.
	if UsesRebase(r) {
		return 1
	}
	// A judge recipe is one model stage at most, and it is read-only: the
	// run's whole work is the judge's own stage over the diff and the
	// findings the launcher left under its prefix (recipe.StepMergeJudge,
	// mergejudge.go), so no implement stage, no review and no loop is
	// counted here whatever the knobs say. Without this the arithmetic below
	// would count the review rounds a run of it never starts, because no
	// review step is there to write their number down.
	if UsesMergeJudge(r) {
		return 1
	}
	implement := 1
	if UsesDigest(r) || UsesCommitReports(r) {
		// Neither of the two deterministic steps is a model's work (digest
		// writes the reports from the records, commit_reports writes the
		// bytes the launcher handed in), so a run of either starts no
		// implement stage whatever the knobs say.
		implement = 0
	}
	first, bounce, rounds, handOver := 0, false, n, false
	adversarial, adversarialBounce := 0, false
	for _, s := range r.Steps {
		switch s.Kind {
		case StepFirstLine:
			first = f
			if s.MaxRounds > 0 {
				first = s.MaxRounds
			}
		case StepReview:
			bounce, handOver = s.Bounce, s.HandOver
			if s.MaxRounds > 0 {
				rounds = s.MaxRounds
			}
		case StepAdversarial:
			adversarial = n3Default
			if s.MaxRounds > 0 {
				adversarial = s.MaxRounds
			}
			adversarialBounce = s.Bounce
		}
	}
	per := 2 // a senior review and its fix
	switch {
	case handOver:
		// The review that hands the run over buys no fix round: the round
		// after it is the one the flag refuses, so each of its rounds is the
		// review alone. A hand_over review cannot bounce (the parser refuses
		// the pair), so there is no cheap loop to add here either.
		per = 1
	case bounce:
		per += 2 * first
	}
	total := implement + 2*first + rounds*per
	if adversarial > 0 {
		per3 := 2 // an adversarial review and its fix
		if adversarialBounce {
			per3 += 2 * first
		}
		total += adversarial * per3
	}
	return total
}

// UsesMergeJudge reports whether r holds the merge judge step, rung 3's whole
// run (recipe.StepMergeJudge, E6). It reads the parsed steps rather than a
// parse-time flag, as UsesRebase and UsesDigest do: a parser that predates the
// step refuses the whole recipe ("is not a step type") instead of dropping it.
func UsesMergeJudge(r *Recipe) bool {
	return slices.ContainsFunc(r.Steps, func(s Step) bool { return s.Kind == StepMergeJudge })
}

// UsesDiagnose reports whether r holds the diagnose step, the cloud tier of
// `fugaro troubleshoot`'s own work (docs/troubleshoot-cloud, D2402). It reads
// the parsed steps rather than a parse-time flag, for the same reason
// UsesDigest does: a parser that predates the step refuses the whole recipe
// ("is not a step type") instead of dropping it.
//
// It is read by the launch gate: the tier's pre-flight chooses diagnoseSince
// when the recipe holds the step (internal/cli/recipes_skew.go), so a
// diagnosis launched against a job image whose runner predates the release
// that executes it is refused at the person's terminal, naming that release
// and `fugaro image refresh`, rather than reaching the runner's own refusal
// (internal/runner) with the launch already paid for.
func UsesDiagnose(r *Recipe) bool {
	return slices.ContainsFunc(r.Steps, func(s Step) bool { return s.Kind == StepDiagnose })
}

// TroubleshootRecipe is the catalog recipe one cloud diagnosis runs
// (docs/troubleshoot-cloud, D2402): a mode: artifact recipe whose single step
// is the diagnose one, so the run's deliverable is the two files that step
// writes rather than a pull request.
const TroubleshootRecipe = "troubleshoot"

// The recipe's own bounds (D2407). The output bound is read by the runner's
// diagnose stage and by the tier's launch; the turn bound is read by neither,
// which its own comment says. They are the recipe's rather than the runner's
// for the reason internal/runner's runDigestReviewerMaxOutput is the report
// run's: a diagnosis is one small question about one failure, so it is bounded
// far below any run — and a stage's model call reserves its whole output
// allowance at the gateway before it spends anything, so a bound on that
// reservation is what keeps the call inside the tier's own cap.
const (
	// TroubleshootMaxTurns is the most turns a diagnosis should take: a
	// diagnosis reads a bundle and answers once, and eight turns is enough for
	// the reading and the answer. It is D2407's bound written down, and nothing
	// reads it: the cloud stage's call goes through internal/agent, whose
	// Request carries no turn knob and whose Args passes no --max-turns (only
	// the local tier's child gets one, from its own cap in
	// internal/diagnoseagent), so a cloud diagnosis is bounded by the per-call
	// output reservation below, the run's own lease and the stage's timeout.
	// Enforcing it is a behaviour of its own — a turns field on agent.Request
	// and --max-turns in agent.Args, set from here for this stage — and the
	// owner's to decide; schemas/recipe.schema.json says the same.
	TroubleshootMaxTurns = 8
	// TroubleshootReviewerMaxOutput is the per-call output the stage's model
	// may reserve: 10000 tokens is $0.20 at the reviewer model's $20/M output
	// rate, and the $0.05 that leaves inside D2407's $0.25 cap is the room the
	// same call's input side needs, because the gateway bounds a call at its
	// input plus its output (pricing.WorstCase, which adds the request
	// overhead and the prompt's own tokens). A reservation holding the whole
	// cap would halt every call of this stage at the ledger with nothing
	// spent, which is the shape internal/runner's runDigestReviewerMaxOutput
	// avoids by sitting below a report run's per-run cap rather than equal to
	// it.
	TroubleshootReviewerMaxOutput int64 = 10000
)

// UsesV07 reports whether r uses a key added in 0.7.0 (the image gate). It
// decides by key presence (Recipe.newKeys, set while parsing), not by the
// parsed value: a 0.6 parser refuses use_when, mode, roles.coder, a check
// step and review.bounce outright, even when the value given looks like a
// no-op (mode: implement, use_when: "").
func UsesV07(r *Recipe) bool {
	return r.newKeys
}
