package task

import (
	"strings"
	"unicode/utf8"
)

// MinEnding is the shortest ending of a run ID that names a run: four
// characters, the length of the short form itself (docs/short-run-ids,
// D1701). A minimum rather than "any unique ending" because a one- or
// two-character ending that is unique today names another run tomorrow, and
// the command that takes one — cancel — is destructive. The console's copy of
// the rule carries the same number, in the case table the two share
// (testdata/shortid_cases.json).
const MinEnding = 4

// Ref is one run as a renderer shows it: the repository's storage slug and the
// run's full ID. The pair is what the collision rule works on, not the ID
// alone, because one run ID can exist in two repositories (runstore's Locate
// reports that as ambiguous) and no ending of it tells those two rows apart
// (F15).
type Ref struct {
	Slug  string
	RunID string
}

// ShortRun is a run ID's short form: its last MinEnding characters, or the
// whole string when it is shorter. A run ID is a launch stamp plus four random
// hex digits (20261108-115248-3bb2), so every prefix of one is shared — two
// runs launched in the same second differ only in the tail — and the tail is
// what a person reads, says out loud and types back (spec.md §1).
//
// It counts runes and not bytes: the argument is text a surface displays, and
// a string that is no run ID at all (an ID from a record that did not parse)
// must not be cut through a character on its way to a terminal. A run ID is
// ASCII, so the two agree on every value this rule was written for.
func ShortRun(id string) string { return lastRunes(id, MinEnding) }

// EndsWith reports whether id ends with ending, an ending a lookup may accept:
// at least MinEnding characters of the run ID's own tail. A shorter ending is
// refused even when it is unique and even when it is the whole of a string too
// short to have a short form, because no surface shows one: a caller that took
// "b2" for a run would resolve a name a person never sees (D1701).
func EndsWith(id, ending string) bool {
	return utf8.RuneCountInString(ending) >= MinEnding && strings.HasSuffix(id, ending)
}

// DisplayRuns is the one collision rule (spec.md §3, D1706): for every ref it
// returns the shortest ending of that run's ID that no other ref's ID also
// ends with, never shorter than MinEnding and extended one character at a time
// from the end. The extension crosses the dash and reaches into the launch
// stamp, because two runs sharing four hex digits share the dash before them
// too (F1). A run whose ID is shorter than the minimum is shown whole.
//
// The endings are computed over exactly the refs passed in: a collision is a
// property of what is shown together and not of the bucket (F15), so one run
// may be shown as `3bb2` in one table and as `8-3bb2` in another.
//
// Two refs carrying the same RunID in different repositories cannot be told
// apart by any ending — their IDs are equal — so each is shown as
// `<slug>/<ending>`: the repository is what distinguishes those rows, and the
// ending is still the shortest one their shared ID needs against the others.
func DisplayRuns(refs []Ref) map[Ref]string {
	out := make(map[Ref]string, len(refs))
	for _, r := range refs {
		ending := shortestEnding(refs, r)
		if sharedRunID(refs, r) {
			ending = r.Slug + "/" + ending
		}
		out[r] = ending
	}
	return out
}

// shortestEnding is the shortest ending of r's ID that no ref with a DIFFERENT
// ID also ends with. Refs sharing r's ID are skipped: no ending tells identical
// IDs apart, and DisplayRuns answers that case with the repository instead.
func shortestEnding(refs []Ref, r Ref) string {
	n := MinEnding
	for n < utf8.RuneCountInString(r.RunID) && collidesAt(refs, r, n) {
		n++
	}
	return lastRunes(r.RunID, n)
}

// collidesAt reports whether another ref's ID ends with the same n characters
// as r's does.
func collidesAt(refs []Ref, r Ref, n int) bool {
	ending := lastRunes(r.RunID, n)
	for _, q := range refs {
		if q.RunID != r.RunID && lastRunes(q.RunID, n) == ending {
			return true
		}
	}
	return false
}

// sharedRunID reports whether another ref carries r's own run ID.
func sharedRunID(refs []Ref, r Ref) bool {
	for _, q := range refs {
		if q != r && q.RunID == r.RunID {
			return true
		}
	}
	return false
}

// lastRunes is the last n runes of s, or all of s when it is shorter.
func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
