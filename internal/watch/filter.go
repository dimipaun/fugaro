package watch

import (
	"sort"
	"time"
)

// Filter hides finished rows that nobody needs to see any more (design
// generic-tool §10.3): the dashboard stays about what needs attention, not
// every run that ever happened. The zero Filter hides everything that is not
// a failure, so every caller sets Keep, KeepCount and FailedKeep.
type Filter struct {
	All        bool
	Keep       time.Duration // default 6h
	KeepCount  int           // default 15
	FailedKeep time.Duration // 24h, not a flag
}

// Apply hides v's finished rows per f and returns the result and how many it
// hid. A failure is kept when it is not acknowledged and younger than
// FailedKeep; a success, already newest first, is kept while it is younger
// than Keep and fewer than KeepCount successes have been kept so far. All
// bypasses every rule. acked may be nil (nothing is acknowledged).
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
		successes := 0
		for _, r := range b.Finished {
			var keep bool
			if r.Failed {
				keep = f.All || (!acked(b.Slug, r.Run) && r.Age < f.FailedKeep)
			} else {
				keep = f.All || (r.Age < f.Keep && successes < f.KeepCount)
				if keep {
					successes++
				}
			}
			if keep {
				kept = append(kept, r)
			} else {
				hidden++
			}
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
