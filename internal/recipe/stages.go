package recipe

// StageBound is the most stages a run of r can start with first-line rounds f
// and review rounds n (the knobs), implement included. Check steps are
// runner commands with their own timeout, so they are not model stages and
// do not count.
func StageBound(r *Recipe, f, n int) int {
	if r.Mode == ModeReview {
		return 1
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
	return 1 + 2*first + rounds*per
}

// UsesV07 reports whether r uses a key added in 0.7.0 (the image gate).
func UsesV07(r *Recipe) bool {
	if r.UseWhen != "" || r.CoderIsReviewer || r.Mode == ModeReview {
		return true
	}
	for _, s := range r.Steps {
		if s.Kind == StepCheck || s.Bounce {
			return true
		}
	}
	return false
}
