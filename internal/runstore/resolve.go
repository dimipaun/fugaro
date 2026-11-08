package runstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/task"
)

// ErrTooShort means an ending shorter than task.MinEnding. It is a user error
// and never a "not found": no surface shows an ending shorter than the short
// form itself, so a lookup that accepted one would resolve a name a person
// never typed out of something they did, and the command the rule exists for
// — cancel — is destructive (D1701).
var ErrTooShort = errors.New("run ID ending is too short")

// Match is one run an ending resolved to: the repository and the run's full
// ID, which is what a caller needs to open the run or to name it back to the
// person, and whether the run is one of the active ones.
//
// Active tells a caller that the run came from the live feed rather than from
// the recent window, which is what `cancel` needs to list an ending among the
// active runs only (D1710): a run that merely ran in the last 7 days must be
// tellable from one that is running now. It is false for a run the caller
// named exactly (a full ID or `<slug>/<run-id>`), which goes to Locate and is
// never looked up in the feed.
type Match struct {
	Slug   string
	RunID  string
	Active bool
}

// ResolveEnding resolves ending — the last task.MinEnding or more characters
// of a run ID, the form every surface displays and a person types back — to
// the runs it names (spec.md §2, D1701, D1702).
//
// The active runs are asked first, through active, and the recent window only
// when they hold no match: a run that is running now wins over a finished run
// whose ID ends the same way, because the live one is the run the person is
// looking at (D1702). The window is since, the caller's — `runs ls`'s seven
// days — and a run minted before it is not found by an ending however unique
// that ending is (F16); its full ID still is.
//
// active is the caller's own read of the live feed, nil when the caller has
// none. The resolver trusts the set it is given: confirming that a feed entry
// names a run that exists — a valid run ID with a task.json in the bucket —
// is the supplier's job (F6), because the resolver cannot tell a feed entry
// that was never a run from one whose objects it may not read. An error from
// active is returned as it is, so a lookup that cannot read the feed says so
// instead of narrowing itself silently to the window.
//
// A full run ID and `<slug>/<run-id>` are not endings: they are handed to
// Locate unchanged, errors included, and no match comes back with Locate's
// ambiguity — its own message names the repositories.
//
// The matches come back in the order the surfaces show them (start time,
// then ID). Exactly one match is a resolution; several are ErrAmbiguous with
// the matches returned beside the error, because a collision is never
// resolved by picking one (D1700); none is ErrNotFound.
func ResolveEnding(ctx context.Context, b *blob.Bucket, active func(context.Context) ([]task.Ref, error), ending string, since time.Time) ([]Match, error) {
	if strings.Contains(ending, "/") || runIDRE.MatchString(ending) {
		slug, runID, err := Locate(ctx, b, ending)
		if err != nil {
			return nil, err
		}
		return []Match{{Slug: slug, RunID: runID}}, nil
	}
	if n := utf8.RuneCountInString(ending); n < task.MinEnding {
		return nil, fmt.Errorf("%q is %d characters; an ending needs at least %d: %w", ending, n, task.MinEnding, ErrTooShort)
	}
	if active != nil {
		refs, err := active(ctx)
		if err != nil {
			return nil, fmt.Errorf("reading the active runs: %w", err)
		}
		if matches := endingMatches(refs, ending, true); len(matches) > 0 {
			return oneOf(ending, matches)
		}
	}
	slugs, err := ListSlugs(ctx, b)
	if err != nil {
		return nil, err
	}
	var matches []Match
	for _, slug := range slugs {
		ids, err := ListRunIDs(ctx, b, slug, since)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if task.EndsWith(id, ending) {
				matches = append(matches, Match{Slug: slug, RunID: id})
			}
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no run ending %q among the active runs and the runs since %s: %w", ending, since.UTC().Format(time.RFC3339), ErrNotFound)
	}
	return oneOf(ending, matches)
}

// endingMatches is the refs whose run IDs end with ending, as matches; active
// marks the set they came from.
func endingMatches(refs []task.Ref, ending string, active bool) []Match {
	var out []Match
	for _, r := range refs {
		if task.EndsWith(r.RunID, ending) {
			out = append(out, Match{Slug: r.Slug, RunID: r.RunID, Active: active})
		}
	}
	return out
}

// oneOf returns the single match, or every match in the order a person reads
// them with ErrAmbiguous. A caller lists those rather than choosing, so the
// order is the one the surfaces use — start time, then ID (watch's own) — and
// not the order the feed or the listing happened to come in.
func oneOf(ending string, matches []Match) ([]Match, error) {
	slices.SortFunc(matches, func(a, b Match) int {
		if c := strings.Compare(a.RunID, b.RunID); c != 0 {
			return c
		}
		return strings.Compare(a.Slug, b.Slug)
	})
	if len(matches) == 1 {
		return matches, nil
	}
	refs := make([]string, len(matches))
	for i, m := range matches {
		refs[i] = m.Slug + "/" + m.RunID
	}
	return matches, fmt.Errorf("ending %q matches %d runs (%s): %w", ending, len(matches), strings.Join(refs, ", "), ErrAmbiguous)
}
