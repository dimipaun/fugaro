package recipe

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
// which is the direction a ceiling may err in.
//
// A digest recipe counts no implement stage either: its first step is the
// runner's own work, which writes the run reports with no model call
// (run-digest), so the review loop after it is all a run of it can record.
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
	if r.Mode == ModeReview || r.Mode == ModeArtifact {
		return 1
	}
	// A rebase recipe is one model stage at most: the conflicts are the only
	// thing in it a model is asked about, and a rebase that resolves them
	// without one -- or refuses before the stage at all -- costs fewer
	// stages still.
	if UsesRebase(r) {
		return 1
	}
	implement := 1
	if UsesDigest(r) {
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

// UsesV07 reports whether r uses a key added in 0.7.0 (the image gate). It
// decides by key presence (Recipe.newKeys, set while parsing), not by the
// parsed value: a 0.6 parser refuses use_when, mode, roles.coder, a check
// step and review.bounce outright, even when the value given looks like a
// no-op (mode: implement, use_when: "").
func UsesV07(r *Recipe) bool {
	return r.newKeys
}
