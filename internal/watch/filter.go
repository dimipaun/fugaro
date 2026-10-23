package watch

import (
	"sort"
	"time"
)

// FinishedKeep is how long a finished run stays listed by default: three
// minutes (owner spec 2026-10-22, item 3). A run that has finished is noise
// once the person watching has seen how it went, so it leaves the list by
// itself — succeeded, failed and cancelled alike — and the x key dismisses
// one at once. a (--all) shows the ones the window or a dismissal took away
// again.
const FinishedKeep = 3 * time.Minute

// Filter hides finished rows that nobody needs to see any more (design
// generic-tool §10.3): the dashboard stays about what needs attention, not
// every run that ever happened. The zero Filter hides everything, so every
// caller sets Keep, KeepCount and FailedKeep.
type Filter struct {
	All       bool
	Keep      time.Duration // FinishedKeep (3m); --keep widens it
	KeepCount int           // default 15
	// FailedKeep is a FLOOR under a failed row's window, not a flag: a
	// failure is the row a person most needs to see, so it is never kept for
	// shorter than a success of the same age. --keep raises a failure's
	// window with a success's — the 0.10.2 fix of #950, where a fixed 3m
	// here made `watch --keep 24h` show a 10m-old success and hide a 10m-old
	// failure (raising the window made a failure LESS visible). A caller
	// that has no floor of its own passes FinishedKeep.
	FailedKeep time.Duration
}

// window is how long a finished row stays shown: --keep, except that a
// failed row keeps the wider of --keep and FailedKeep.
func (f Filter) window(r RunRow) time.Duration {
	if r.Failed && f.FailedKeep > f.Keep {
		return f.FailedKeep
	}
	return f.Keep
}

// Apply hides v's finished rows per f and returns the result and how many it
// hid. A dismissed run is hidden whatever its status — x means "I have seen
// this one" (owner spec 2026-10-22, item 4). Every other finished row,
// whatever its status, is kept while it is younger than its window (window:
// --keep, never shorter for a failure than for a success) and fewer than
// KeepCount finished rows have been kept before it; rows arrive newest
// first, so KeepCount keeps the newest ones and a failure is displaced by a
// newer row exactly as a success is (0.10.2 fix of #950: --keep and
// --keep-count reach failed rows too). All bypasses every rule. acked may be
// nil (nothing is dismissed).
func (f Filter) Apply(v View, acked func(slug, run string) bool) (View, int) {
	if acked == nil {
		acked = func(string, string) bool { return false }
	}
	hidden := 0
	repos := make([]RepoBlock, len(v.Repos))
	copy(repos, v.Repos)
	for i, b := range repos {
		if len(b.Finished) == 0 {
			continue
		}
		var kept []RunRow
		shown := 0 // finished rows kept so far in this block, failures included
		for _, r := range b.Finished {
			if f.All {
				kept = append(kept, r)
				continue
			}
			keep := !acked(b.Slug, r.Run) && r.Age < f.window(r) && shown < f.KeepCount
			if !keep {
				hidden++
				continue
			}
			shown++
			kept = append(kept, r)
		}
		repos[i].Finished = kept
	}
	v.Repos = repos
	return v, hidden
}

// ReadyForReview lists every finished row across v whose Outcome is "ready"
// (a senior ship and a verified passing test, design generic-tool §10.1),
// newest first. A "draft" outcome (passed review on a draft whose test
// failed) is never listed here.
func ReadyForReview(v View) []RunRow {
	var out []RunRow
	for _, b := range v.Repos {
		for _, r := range b.Finished {
			if r.Outcome == "ready" {
				out = append(out, r)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

// DropEmptyFinishedBlocks removes a block that MergeFinished (or
// MergeQueued) created solely to show finished or queued rows once the
// filter has hidden the last one: Build itself never shows a block with no
// runs, no spend and no kill switch, so a block left with nothing after
// filtering should not linger either.
func DropEmptyFinishedBlocks(v View) View {
	var keep []RepoBlock
	for _, b := range v.Repos {
		if len(b.Runs) > 0 || len(b.Finished) > 0 || b.Counted != 0 || b.Spent != 0 || b.Notional != 0 || b.Kill.On {
			keep = append(keep, b)
		}
	}
	v.Repos = keep
	return v
}
