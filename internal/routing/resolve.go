package routing

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// The resolver (RT1): which entry of the table a model is routed by.
//
// One rule decides it, and it is D3172's — the most specific match wins, an
// exact model id over any prefix and a longer prefix over a shorter one. The
// grammar that says what a pattern claims is not here: config.PatternMatches is
// the one matcher this build has (the gateway's routes judge a model by the same
// function), and a second matcher in this package would be a second answer to
// "does this pattern claim this model", which is the drift the table exists to
// prevent. So this file weighs the matches that matcher reports and nothing
// else.
//
// What a caller may rely on is what the answer is NOT: a table with no matching
// entry answers ErrNoEntry and never "the first entry", because falling back to
// whatever credential is around is the behaviour the table replaces (R1, R22).
// The stub this file replaces (resolve_stub.go) answers the first entry, and
// that difference is RT1's red step.

// resolve is the resolver's body: the entry that routes model, the most specific
// match winning, or ErrNoEntry when no entry claims it.
//
// A winning pattern written twice is refused, and precedence is exactly what
// cannot decide it: an exact id outranks a prefix and a longer prefix outranks a
// shorter one, so two matches tie only when they are one pattern written twice
// (D3172). Answering would serve whichever entry the project happened to write
// first — a chain nobody chose — so the refusal names the pattern and the fix is
// "keep one entry per pattern".
//
// The tie that refuses is the winner's, which is why the loop carries it to the
// end instead of refusing where it is found: twins under a pattern a more
// specific entry beats decide nothing, and refusing there would answer by entry
// order — {claude-*, claude-*, claude-sonnet-*} and {claude-sonnet-*, claude-*,
// claude-*} would disagree on a model both routes to claude-sonnet-*. The refusal
// is about the model asked for and not about the table at rest either: twins that
// claim no model this call names leave this call's answer alone, which is what
// keeps the resolver reporting "I cannot pick between these two" rather than
// "this table is broken", a judgement RT3's checks make.
func resolve(table Table, model string) (Entry, error) {
	best := -1
	tied := false
	for i, entry := range table {
		if !config.PatternMatches(entry.Match, model) {
			continue
		}
		switch {
		case best < 0:
			best, tied = i, false
		case moreSpecific(entry.Match, table[best].Match):
			// A new winner decides the call, so a tie under the pattern it
			// replaced is no longer the answer's.
			best, tied = i, false
		case entry.Match == table[best].Match:
			tied = true
		}
	}
	if best < 0 {
		return Entry{}, fmt.Errorf("%w: %q", ErrNoEntry, model)
	}
	if tied {
		return Entry{}, fmt.Errorf("routing: %q is written twice in the table, so neither entry is the more specific one for %q: keep one entry per pattern", table[best].Match, model)
	}
	return table[best], nil
}

// moreSpecific reports whether pattern a is the more specific match of a model
// that both a and b matched: an exact model id beats any prefix, and of two
// prefixes the longer wins (D3172).
//
// The two are only ever compared when both matched the same model, and that is
// what makes the prefix rule the whole rule: both are the beginning of one model
// id, so the longer one is the narrower claim. An exact id that matched is the
// model itself, so its length would say nothing about it — which is why the two
// kinds are told apart before their lengths ever are.
func moreSpecific(a, b string) bool {
	pa, ga := strings.CutSuffix(a, "*")
	pb, gb := strings.CutSuffix(b, "*")
	if ga != gb {
		return !ga
	}
	return len(pa) > len(pb)
}

// Resolve is assigned here and not in resolve_stub.go, whose init waits for a
// real one (if Resolve == nil). This assignment is unconditional, so the real
// resolver stands whichever order the toolchain presented the two files in: the
// stub's guard finds Resolve taken and leaves it, and an init that ran first
// would be overwritten by this one.
func init() {
	Resolve = resolve
}
