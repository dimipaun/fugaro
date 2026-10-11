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

// UsesV07 reports whether r uses a key added in 0.7.0 (the image gate). It
// decides by key presence (Recipe.newKeys, set while parsing), not by the
// parsed value: a 0.6 parser refuses use_when, mode, roles.coder, a check
// step and review.bounce outright, even when the value given looks like a
// no-op (mode: implement, use_when: "").
func UsesV07(r *Recipe) bool {
	return r.newKeys
}

// NotYetRunnable names, in one sentence, why no runner can yet run r as
// written, or "" when there is no such gap. mode: review is meant to skip
// implement entirely (G12); until the runner that executes it lands
// (generic-tool plan Task 12), a recipe using it would instead have today's
// runner silently run implement regardless (Recipe.Mode is never read),
// which is wrong for exactly the use case the construct exists for. Every
// caller that resolves a recipe for a real launch (the runner's own
// resolution, the CLI's, and fugaro recipes validate/publish) calls this
// and refuses rather than let that happen. A check step (G10, G11) is no
// longer in this gap: Task 11's runner gates the run on it. Remove this
// function entirely, and update the catalog's review-only and pick
// recipes' own use_when texts, once Task 12 lands.
func NotYetRunnable(r *Recipe) string {
	if r.Mode == ModeReview {
		return "mode: review is not yet executed by any runner: it would run implement and open a pull request instead of only reviewing"
	}
	return ""
}
