package policy

import (
	"math"
	"strconv"
	"strings"
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
