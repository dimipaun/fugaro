package rigor

import (
	"slices"

	"github.com/bmatcuk/doublestar/v4"
)

// Select is the areas a run's paths match (D20c, D23), strictest first.
//
// It is D23's answer to a run that touches several areas: the strictest mode
// among the matched rules decides, and every rule that matched is kept so
// the run can record which ones did. Strictest-first means the caller never
// has to order them again, and the first entry is the one whose mode won.
//
// The result is deduplicated by name, keeping the strictest rule of each
// name: the two layers may each carry an area list (D20c), so the same name
// can arrive twice, and a record that printed it twice would be saying two
// rules decided when one idea did.
//
// A path that matches nothing is not an error and produces no rule: the mode
// then stays what the layers below set, which is what a run with no declared
// paths and an empty diff gets.
//
// A pattern the matcher cannot read cannot reach here — Area.Validate
// refuses it at load — so Match's own error is dropped rather than reported;
// a caller that builds an Area by hand gets no match, never a panic.
func Select(areas []Area, paths []string) []Area {
	var out []Area
	at := map[string]int{} // name -> its index in out
	for _, a := range areas {
		if !matches(a.Match, paths) {
			continue
		}
		if i, seen := at[a.Name]; seen {
			// Same name, twice: keep the stricter rule, in place, so the
			// order out is already strictest-first.
			if a.Mode.Rank() > out[i].Mode.Rank() {
				out[i] = a
			}
			continue
		}
		at[a.Name] = len(out)
		out = append(out, a)
	}
	// Stable, so rules of equal rank keep the order they were written in.
	slices.SortStableFunc(out, func(x, y Area) int { return y.Mode.Rank() - x.Mode.Rank() })
	return out
}

// matches reports whether pattern matches any of paths.
func matches(pattern string, paths []string) bool {
	if pattern == "" {
		return false
	}
	for _, p := range paths {
		if ok, err := doublestar.Match(pattern, p); err == nil && ok {
			return true
		}
	}
	return false
}

// ModesOf is the modes of areas, in order: the set Strictest reads to answer
// which of them decided.
func ModesOf(areas []Area) []Mode {
	if len(areas) == 0 {
		return nil
	}
	out := make([]Mode, 0, len(areas))
	for _, a := range areas {
		out = append(out, a.Mode)
	}
	return out
}
