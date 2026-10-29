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
func StageBound(r *Recipe, f, n int) int {
	if r.Mode == ModeReview || r.Mode == ModeArtifact {
		return 1
	}
	implement := 1
	if UsesDigest(r) {
		implement = 0
	}
	first, bounce, rounds := 0, false, n
	for _, s := range r.Steps {
		switch s.Kind {
		case StepFirstLine:
			first = f
			if s.MaxRounds > 0 {
				first = s.MaxRounds
			}
		case StepReview:
			bounce = s.Bounce
			if s.MaxRounds > 0 {
				rounds = s.MaxRounds
			}
		}
	}
	per := 2 // a senior review and its fix
	if bounce {
		per += 2 * first
	}
	return implement + 2*first + rounds*per
}

// UsesV07 reports whether r uses a key added in 0.7.0 (the image gate). It
// decides by key presence (Recipe.newKeys, set while parsing), not by the
// parsed value: a 0.6 parser refuses use_when, mode, roles.coder, a check
// step and review.bounce outright, even when the value given looks like a
// no-op (mode: implement, use_when: "").
func UsesV07(r *Recipe) bool {
	return r.newKeys
}
