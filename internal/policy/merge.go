package policy

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/rigor"
)

func modeRank(m string) int {
	switch m {
	case ModeOff:
		return 1
	case ModeObserve:
		return 2
	case ModeEnforce:
		return 3
	}
	return 0 // unset or unknown: carries no value
}

// layerName names the i-th layer: 0 is the ceiling, 1 the default branch, and
// anything after that the run's branch.
func layerName(i int) string {
	switch i {
	case 0:
		return SourceCeiling
	case 1:
		return SourceDefaultBranch
	}
	return SourceBranch
}

// Merge applies layers in order (the ceiling first, then tighten[0] as the
// default branch, tighten[1] as the run's branch). A later layer may only
// tighten: the smaller positive cap, the stricter mode (enforce > observe >
// off), the intersection of allow-lists. A key a layer leaves unset passes the
// earlier layers through. A value a later layer sets that does not tighten is
// dropped and recorded in Ignored (equal values are not ignored; the ceiling
// is never ignored). Later layers are compared to the running effective value.
func Merge(ceiling Layer, tighten ...Layer) Effective {
	e := Effective{Sources: map[string]string{}}
	layers := append([]Layer{ceiling}, tighten...)
	for i, l := range layers {
		src := layerName(i)

		if l.PerRunUSD > 0 && !math.IsInf(l.PerRunUSD, 0) { // NaN fails > 0; +Inf is unset
			switch {
			case e.PerRunUSD == 0:
				e.PerRunUSD, e.Sources[KeyPerRunUSD] = l.PerRunUSD, src
			case l.PerRunUSD < e.PerRunUSD:
				e.PerRunUSD, e.Sources[KeyPerRunUSD] = l.PerRunUSD, src
			case l.PerRunUSD > e.PerRunUSD:
				e.Ignored = append(e.Ignored, Ignored{KeyPerRunUSD,
					strconv.FormatFloat(l.PerRunUSD, 'f', -1, 64),
					strconv.FormatFloat(e.PerRunUSD, 'f', -1, 64), e.Sources[KeyPerRunUSD]})
			}
		}

		e.tightenCap(&e.PerDayUSD, l.PerDayUSD, KeyPerDayUSD, src)

		// The A/B rate is a bound like the caps above, and the one key of the
		// ab: block that crosses layers: the project layer sets it and a
		// repository may only lower it, to 0, which never samples that
		// repository (D556, D557). A layer that writes no rate passes the
		// earlier layers through, which is a nil ABRate and not a 0 one: the
		// opt-out is the value 0, so the two cannot share the zero value. No
		// layer carries a rate yet, so the rule is unexercised by a real
		// configuration until the launcher that fills it ships (Layer.ABRate).
		if l.ABRate != nil {
			switch {
			case e.ABRate == nil || *l.ABRate < *e.ABRate:
				e.ABRate, e.Sources[KeyABRate] = l.ABRate, src
			case *l.ABRate > *e.ABRate:
				e.Ignored = append(e.Ignored, Ignored{KeyABRate,
					strconv.FormatFloat(*l.ABRate, 'f', -1, 64),
					strconv.FormatFloat(*e.ABRate, 'f', -1, 64), e.Sources[KeyABRate]})
			}
		}

		if l.MaxRunTokens > 0 {
			switch {
			case e.MaxRunTokens == 0 || l.MaxRunTokens < e.MaxRunTokens:
				e.MaxRunTokens, e.Sources[KeyMaxRunTokens] = l.MaxRunTokens, src
			case l.MaxRunTokens > e.MaxRunTokens:
				e.Ignored = append(e.Ignored, Ignored{KeyMaxRunTokens,
					strconv.FormatInt(l.MaxRunTokens, 10),
					strconv.FormatInt(e.MaxRunTokens, 10), e.Sources[KeyMaxRunTokens]})
			}
		}

		e.tightenInt(&e.MaxOutputCoder, l.MaxOutputCoder, KeyMaxOutputCoder, src)
		e.tightenInt(&e.MaxOutputReviewer, l.MaxOutputReviewer, KeyMaxOutputReviewer, src)

		// Governance is the same rule over a different vocabulary: every key
		// is a positive bound whose smaller value is the stricter one, so a
		// layer that writes none of them (the job environment, the published
		// policy node, a repository that has no governance: block) tightens
		// nothing and the ceiling it was handed stands. The rule is asked of
		// the policy hook (hook.go), whose default tightens nothing at all: an
		// untagged build decodes the block and holds no run to it, and the
		// rule itself is ee/governance's (§5.2, D522).
		governanceHook().Tighten(&e, l.Governance, src)

		// The review-level floor is the one key whose "stricter" is a union
		// rather than a comparison: a level a layer requires is a level every
		// layer under it must run, so a later layer can add one and can never
		// take one away (D722, G1). It is also the one key no launcher-written
		// layer is given: the owner's floor arrives on the ceiling, from the
		// policy node (Layer.ReviewFloor).
		if len(l.ReviewFloor) > 0 {
			e.unionReviewFloor(l.ReviewFloor)
		}

		if r := modeRank(l.Mode); r > 0 {
			cur := modeRank(e.Mode)
			switch {
			case r > cur:
				e.Mode, e.Sources[KeyMode] = l.Mode, src
			case r < cur:
				e.Ignored = append(e.Ignored, Ignored{KeyMode, l.Mode, e.Mode, e.Sources[KeyMode]})
			}
		}

		if l.AllowedModels != nil {
			if e.AllowedModels == nil {
				e.AllowedModels, e.Sources[KeyAllowedModels] = dedupe(l.AllowedModels), src
			} else {
				in, running := toSet(l.AllowedModels), toSet(e.AllowedModels)
				kept := make([]string, 0, len(e.AllowedModels))
				for _, m := range e.AllowedModels { // running order is kept
					if in[m] {
						kept = append(kept, m)
					}
				}
				extra := false
				for _, v := range l.AllowedModels {
					if !running[v] {
						extra = true
						break
					}
				}
				prev := e.Sources[KeyAllowedModels]
				if len(kept) < len(e.AllowedModels) {
					e.AllowedModels, e.Sources[KeyAllowedModels] = kept, src
				}
				if extra { // asked for a model the earlier layers had excluded
					e.Ignored = append(e.Ignored, Ignored{KeyAllowedModels,
						strings.Join(dedupe(l.AllowedModels), ","),
						strings.Join(e.AllowedModels, ","), prev})
				}
			}
		}
	}
	if len(e.Sources) == 0 {
		e.Sources = nil
	}
	return e
}

// unionReviewFloor adds l's levels to the effective floor, mode by mode, and
// keeps each mode's levels in the vocabulary's order (Levels). Nothing is
// recorded in Ignored: a union has no loser — the levels a layer asked for
// are all required, and the levels it left out are the ones no layer asked
// for.
//
// Every slice the result holds is built here, so neither the layers' maps nor
// their slices are aliased or written to.
func (e *Effective) unionReviewFloor(l map[rigor.Mode][]Level) {
	for mode, added := range l {
		running := e.Layer.ReviewFloor[mode]
		var out []Level
		for _, lv := range Levels {
			if slices.Contains(added, lv) || slices.Contains(running, lv) {
				out = append(out, lv)
			}
		}
		if len(out) == 0 {
			continue
		}
		if e.Layer.ReviewFloor == nil {
			e.Layer.ReviewFloor = make(map[rigor.Mode][]Level, len(l))
		}
		e.Layer.ReviewFloor[mode] = out
	}
}

// TightenDuration is tightenDuration for the packages outside this one:
// ee/governance's Tighten and its tightenTier are the governance policy's
// (plan task E2.3, design ee-boundary-moves.md §5.2) and cannot reach an
// unexported method, so the moved code calls the rule here rather than keeping
// a copy of it that would drift from this one.
func (e *Effective) TightenDuration(cur *time.Duration, v time.Duration, key, src string) {
	e.tightenDuration(cur, v, key, src)
}

// tightenDuration is tightenInt for a stage timeout: a duration that is not
// positive is unset.
func (e *Effective) tightenDuration(cur *time.Duration, v time.Duration, key, src string) {
	if v <= 0 {
		return
	}
	switch {
	case *cur <= 0 || v < *cur:
		*cur, e.Sources[key] = v, src
	case v > *cur:
		e.Ignored = append(e.Ignored, Ignored{key, v.String(), cur.String(), e.Sources[key]})
	}
}

// TightenCap is tightenCap for the packages outside this one, on the same
// terms as TightenDuration: the governance policy moves to ee/governance and
// calls the one rule rather than a copy of it.
func (e *Effective) TightenCap(cur *float64, v float64, key, src string) {
	e.tightenCap(cur, v, key, src)
}

// tightenCap is tightenInt for a positive float limit: a dollar cap, or a
// stage budget's fraction of one. NaN, +Inf, zero and negatives are unset.
func (e *Effective) tightenCap(cur *float64, v float64, key, src string) {
	if !(v > 0) || math.IsInf(v, 0) {
		return
	}
	switch {
	case *cur == 0 || v < *cur:
		*cur, e.Sources[key] = v, src
	case v > *cur:
		e.Ignored = append(e.Ignored, Ignored{key, strconv.FormatFloat(v, 'f', -1, 64),
			strconv.FormatFloat(*cur, 'f', -1, 64), e.Sources[key]})
	}
}

// TightenInt is tightenInt for the packages outside this one, on the same
// terms as TightenDuration.
func (e *Effective) TightenInt(cur *int64, v int64, key, src string) {
	e.tightenInt(cur, v, key, src)
}

// tightenInt applies a layer's positive limit v to *cur: smaller wins, a
// larger one is ignored and recorded, 0 and below are unset.
func (e *Effective) tightenInt(cur *int64, v int64, key, src string) {
	if v <= 0 {
		return
	}
	switch {
	case *cur == 0 || v < *cur:
		*cur, e.Sources[key] = v, src
	case v > *cur:
		e.Ignored = append(e.Ignored, Ignored{key, strconv.FormatInt(v, 10),
			strconv.FormatInt(*cur, 10), e.Sources[key]})
	}
}

func toSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

// dedupe returns a copy of s without repeats, order kept; non-nil for non-nil s.
func dedupe(s []string) []string {
	out := make([]string, 0, len(s))
	seen := map[string]bool{}
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
