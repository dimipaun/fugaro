package followup

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// Limits bound what a follow-up's agent gets. MaxThreads counts inline
// comments (each on an unresolved review thread), which take at most that
// many of the MaxComments slots; review summaries and general comments
// fill the rest. MaxBodyBytes clips each body, MaxTotalBytes all of them.
type Limits struct{ MaxComments, MaxThreads, MaxBodyBytes, MaxTotalBytes int }

// DefaultLimits are the bounds a follow-up uses.
var DefaultLimits = Limits{MaxComments: 60, MaxThreads: 40, MaxBodyBytes: 4 << 10, MaxTotalBytes: 32 << 10}

// maxUntrustedNames caps Selection.UntrustedAuthors.
const maxUntrustedNames = 20

// Reasons a comment is left out, the keys of Selection.Omitted.
const (
	omitResolved   = "resolved"
	omitFugaro     = "fugaro"
	omitSelf       = "self"
	omitUntrusted  = OmitUntrusted
	omitDeleted    = "deleted"
	omitBeforeTime = "before_since"
	omitEmpty      = "empty"
	omitOverLimit  = "over_limit"
)

// OmitUntrusted is Selection.Omitted's key for comments by authors
// outside the trusted list.
const OmitUntrusted = "untrusted_author"

// Selection is what the agent gets. Omitted counts the drops by reason:
// "resolved", "fugaro", "self", "untrusted_author", "deleted",
// "before_since", "empty", "over_limit".
type Selection struct {
	Since                time.Time
	Comments             []gitprov.Comment // bodies redacted and clipped; rendered oldest first
	Authors              map[string]int    // display name → comments kept
	UntrustedAuthors     []string          // sorted, unique, at most 20
	UntrustedAuthorCount int               // how many distinct untrusted authors were dropped, beyond the 20 named too
	Omitted              map[string]int
	MarkersFromAnyone    bool // no comment was SelfKnown: markers honoured from every author
}

// Select picks the comments a follow-up acts on: unresolved review threads
// whatever their age, and review summaries and general comments created at
// or after since, by authors t allows. Fugaro's own comments, deleted and
// empty ones are dropped. Over l's bounds the oldest go first, so the
// newest remarks survive. Each body is cleaned of NUL bytes and invalid
// UTF-8 and redacted, both ways round (so a secret holding a CRLF matches
// before cleaning, and one split by a NUL after), then clipped: after
// redaction, so a secret straddling the cut is never half-kept.
//
// redact is required (Select panics on nil). It must be the run's
// agent.RedactFunc, built after agent.BuildEnv has registered every
// secret: RedactFunc copies the secret list when it is called.
func Select(all []gitprov.Comment, since time.Time, t Trust, l Limits, redact func(string) string) Selection {
	mustRedact(redact)
	sel := Selection{
		Since:             since,
		Authors:           map[string]int{},
		Omitted:           map[string]int{},
		MarkersFromAnyone: !anySelfKnown(all),
	}
	untrusted := map[string]bool{}
	var threads, others []gitprov.Comment
	for _, c := range all {
		reason := ""
		switch {
		case c.Deleted:
			reason = omitDeleted
		case c.Kind == gitprov.CommentInline && c.Resolved:
			reason = omitResolved
		case isFugaros(c, sel.MarkersFromAnyone):
			reason = omitFugaro
		case t.isSelf(c):
			reason = omitSelf
		case c.Kind != gitprov.CommentInline && c.CreatedAt.Before(since):
			reason = omitBeforeTime
		case strings.TrimSpace(cleanText(c.Body)) == "":
			reason = omitEmpty
		case !t.Allows(c):
			reason = omitUntrusted
			untrusted[displayName(c)] = true
		}
		if reason != "" {
			sel.Omitted[reason]++
			continue
		}
		c.Author = displayName(c)
		c.Path = oneLine(c.Path, 0)
		c.Body = clip(redact(cleanText(redact(c.Body))), l.MaxBodyBytes)
		if c.Kind == gitprov.CommentInline {
			threads = append(threads, c)
		} else {
			others = append(others, c)
		}
	}

	// Over a bound, the oldest go first.
	byAge := func(cs []gitprov.Comment) {
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].CreatedAt.Before(cs[j].CreatedAt) })
	}
	byAge(threads)
	byAge(others)
	threads = newest(threads, min(l.MaxThreads, l.MaxComments), sel.Omitted)
	others = newest(others, l.MaxComments-len(threads), sel.Omitted)
	kept := slices.Concat(threads, others)
	byAge(kept)
	total := 0
	for _, c := range kept {
		total += len(c.Body)
	}
	for len(kept) > 0 && total > l.MaxTotalBytes {
		total -= len(kept[0].Body)
		kept = kept[1:]
		sel.Omitted[omitOverLimit]++
	}

	sel.Comments = kept
	for _, c := range kept {
		sel.Authors[c.Author]++
	}
	if len(untrusted) > 0 {
		names := make([]string, 0, len(untrusted))
		for n := range untrusted {
			names = append(names, n)
		}
		slices.Sort(names)
		sel.UntrustedAuthorCount = len(names)
		sel.UntrustedAuthors = names[:min(len(names), maxUntrustedNames)]
	}
	return sel
}

// newest keeps the last n of cs (sorted oldest first), counting the rest
// in omitted as over the limit.
func newest(cs []gitprov.Comment, n int, omitted map[string]int) []gitprov.Comment {
	n = max(n, 0)
	if len(cs) <= n {
		return cs
	}
	omitted[omitOverLimit] += len(cs) - n
	return cs[len(cs)-n:]
}

// FugaroRuns are the run IDs named by Fugaro's comments, in the order the
// comments come, each once: Self comments only, or every comment when no
// comment is SelfKnown (the identity lookup failed). Comments without a
// run ID, such as an old not-ready note, are skipped.
func FugaroRuns(all []gitprov.Comment) []string {
	anyone := !anySelfKnown(all)
	var runs []string
	for _, c := range all {
		if !c.Self && !anyone {
			continue
		}
		if id, ok := gitprov.FugaroRun(c.Body); ok && id != "" && !slices.Contains(runs, id) {
			runs = append(runs, id)
		}
	}
	return runs
}

// mustRedact panics when redact is missing: a forgotten redactor would
// otherwise ship comment text unredacted.
func mustRedact(redact func(string) string) {
	if redact == nil {
		panic("followup: redact is required; build it with agent.RedactFunc after agent.BuildEnv")
	}
}

func anySelfKnown(all []gitprov.Comment) bool {
	return slices.ContainsFunc(all, func(c gitprov.Comment) bool { return c.SelfKnown })
}

// isFugaros reports whether c is a comment Fugaro posted: it carries a
// marker or an old report heading, and its author is Fugaro's identity
// (or anyone, when that identity is unknown). Anyone can type a marker.
func isFugaros(c gitprov.Comment, markersFromAnyone bool) bool {
	if !c.Self && !markersFromAnyone {
		return false
	}
	_, ok := gitprov.FugaroRun(c.Body)
	return ok
}

// displayName is c's author as one line of at most 64 runes, falling back
// to the account ID.
func displayName(c gitprov.Comment) string {
	for _, n := range []string{c.Author, c.AuthorID} {
		if n = oneLine(n, maxNameRunes); n != "" {
			return n
		}
	}
	return "unknown"
}
