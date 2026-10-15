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

// UsesV08 reports whether r uses a construct added in 0.8.0 (the artifact
// run's image gate): mode: artifact, an outputs: block, or a plan or render
// step. It decides by presence (Recipe.newKeysV08), not by the parsed
// value, for the same reason UsesV07 does: a 0.7 parser refuses all three
// outright, so a mode: artifact recipe whose outputs block holds nothing
// but its globs still needs a 0.8 runner. A recipe that uses a 0.8.0
// construct always uses a 0.7.0 one too (mode is one), so UsesV08 implies
// UsesV07; the two are separate gates because the images they need are
// tagged separately (cli/recipes_skew.go).
func UsesV08(r *Recipe) bool {
	return r.newKeysV08
}
