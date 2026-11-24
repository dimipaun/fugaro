package rigor

import (
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Select is the areas a run's repo and paths match (D20c, D23), strictest
// first.
//
// It is D23's answer to a run that touches several areas: the strictest level
// among the matched rules of one layer decides, and every rule that matched is
// kept so the run can record which ones did. Strictest-first means the caller
// never has to order them again, and the first entry carries the strictest
// level of them. Which rule decided a resolution is a different question —
// within one layer it is this first entry, and across the two layers it is the
// narrower layer's own answer (Resolve reads the lists apart).
//
// repo is the run's repository in the "owner/name" spelling a task's own Repo
// carries, and it is what a project layer's members are matched on (LD4): a
// member names a repository, and only that repository's runs are in the area
// — a whole-repo member matches whatever paths the run declared, and a member
// with paths matches when one of them matches a path the run touched. The
// repository's own rules are globs and do not read repo at all; an empty repo
// (a caller that has none, as every caller had before members existed) matches
// no member and leaves those rules exactly as they were.
//
// The result is deduplicated by name, keeping the strictest rule of each
// name: the two layers may each carry an area list (D20c), so the same name
// can arrive twice, and a record that printed it twice would be saying two
// rules decided when one idea did.
//
// A path that matches nothing is not an error and produces no rule: the level
// then stays what the layers below set, which is what a run with no declared
// paths and an empty diff gets.
//
// A pattern the matcher cannot read cannot reach here — Area.Validate and
// Area.ValidateProject refuse it at load — so Match's own error is dropped
// rather than reported; a caller that builds an Area by hand gets no match,
// never a panic.
func Select(areas []Area, repo string, paths []string) []Area {
	var out []Area
	at := map[string]int{} // name -> its index in out
	for _, a := range areas {
		if !a.matches(repo, paths) {
			continue
		}
		if i, seen := at[a.Name]; seen {
			// Same name, twice: keep the stricter rule, in place, so the
			// order out is already strictest-first.
			if a.Level.Rank() > out[i].Level.Rank() {
				out[i] = a
			}
			continue
		}
		at[a.Name] = len(out)
		out = append(out, a)
	}
	// Stable, so rules of equal rank keep the order they were written in.
	slices.SortStableFunc(out, func(x, y Area) int { return y.Level.Rank() - x.Level.Rank() })
	return out
}

// matches reports whether one rule is in effect for a run of repo touching
// paths: the rule's own glob, or any of its members' (LD4). One selector
// matching is enough — a project rule that carries both a match and members
// is a union of the two, which is what each of them being written beside the
// other says.
func (a Area) matches(repo string, paths []string) bool {
	if a.Match != "" && matches(a.Match, paths) {
		return true
	}
	return a.matchesMember(repo, paths)
}

// matchesMember is matches' member half alone: the rule is in effect for this
// run through a member naming repo whose paths match (a member with no paths
// is the whole repository, so it matches whatever the run declared). Resolve
// reads the two halves apart (D3294): a rule that matched through a member
// naming this repository ranks above the project's own entry for it, while a
// rule that matched through its Match alone answers for every repository of
// the project and keeps LD4's rank below the entry.
func (a Area) matchesMember(repo string, paths []string) bool {
	if repo == "" {
		return false
	}
	for _, m := range a.Members {
		if m.Repo != repo {
			continue
		}
		if len(m.Paths) == 0 {
			// A whole-repo member: the area is the repository itself, so
			// every path of it is in the area, declared ones or none.
			return true
		}
		for _, p := range m.Paths {
			if matches(p, paths) {
				return true
			}
		}
	}
	return false
}

// namesRepo reports whether the rule names repo at all — one of its members is
// that repository, whatever paths of it the member lists and whether they
// match. It is a property of the rule rather than of a run, and it is the
// question D3294 ranks a project rule by: a rule that names this repository
// answers for a named part of it, so it ranks above the project's entry for
// the whole of it, and Configured asks the same question to decide whether the
// rule is about this repository at all.
//
// A repository the rule does not name is unaffected: a member list naming
// another repository is that repository's business, and a rule with no members
// at all is in effect for every repository through its Match (Area.Validate
// refuses a project rule with neither).
func (a Area) namesRepo(repo string) bool {
	if repo == "" {
		return false
	}
	for _, m := range a.Members {
		if m.Repo == repo {
			return true
		}
	}
	return false
}

// projectWideAmong reports whether any of areas is in effect for every
// repository of the project: it carries a Match, the glob D20c shipped and
// still reads beside its members, which is a claim about the project and not
// about one of its repositories. It is what Configured reads as the area half
// of "the project layer opted this repository in".
func projectWideAmong(areas []Area) bool {
	for _, a := range areas {
		if strings.TrimSpace(a.Match) != "" {
			return true
		}
	}
	return false
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

// RigorsOf is the levels of areas, in order: the set Strictest reads to answer
// which of them decided.
func RigorsOf(areas []Area) []Rigor {
	if len(areas) == 0 {
		return nil
	}
	out := make([]Rigor, 0, len(areas))
	for _, a := range areas {
		out = append(out, a.Level)
	}
	return out
}
