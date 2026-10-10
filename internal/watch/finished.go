package watch

import (
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// FinishedRun is a run whose result.json exists, read from the runs bucket
// by the same scanner as queued runs (design generic-tool §10.1).
type FinishedRun struct {
	Run, Slug, Title, Workflow string
	Status                     string // runstore status: succeeded, failed, halted, cancelled, infra_error
	Outcome                    string // ready, draft, none, reviewed
	PRNumber                   int
	PRURL                      string
	FinishedAt                 time.Time
}

// cleanURL is url made safe to show: only an https:// URL of at most 200
// bytes, after clean; anything else (a scheme watch should never link to,
// or bucket text past the length a real URL needs) becomes "", since a PR
// URL is bucket text and so untrusted.
func cleanURL(url string) string {
	s := clean(url)
	if !strings.HasPrefix(s, "https://") || len(s) > 200 {
		return ""
	}
	return s
}

// isFailure reports whether status, a FinishedRun's runstore status, is one
// of the design's failure statuses (generic-tool §10.3, the retention rule
// a later task uses): failed, halted or infra_error. succeeded and
// cancelled are not failures: a cancelled run was withdrawn, not botched.
func isFailure(status string) bool {
	switch status {
	case string(runstore.StatusFailed), string(runstore.StatusHalted), string(runstore.StatusInfraError):
		return true
	}
	return false
}

// finishedRow is f as a RunRow, aged as of now.
func finishedRow(f FinishedRun, now time.Time) RunRow {
	return RunRow{
		Run: clean(f.Run), Slug: budget.Key(f.Slug), Title: dash(clean(f.Title)), Stage: clean(f.Status),
		Round: "-", Verify: "-", Models: "-", Auth: "-", Workflow: clean(f.Workflow),
		Age: max(now.Sub(f.FinishedAt), 0), HasAge: true,
		Finished: true, Failed: isFailure(f.Status), Outcome: clean(f.Outcome),
		PRURL: cleanURL(f.PRURL), PRNumber: f.PRNumber, StartedAt: f.FinishedAt.UnixMilli(),
	}
}

// MergeFinished adds fin as extra, finished rows of v's repository blocks
// (creating a block for a repository with no spend, agent or kill switch of
// its own yet, named and ordered the same way MergeQueued would), newest
// first. A run already shown live (its registry entry has not yet been
// deleted) is skipped.
func MergeFinished(v View, cfg Config, fin []FinishedRun, now time.Time) View {
	live := map[string]bool{}
	for _, b := range v.Repos {
		for _, r := range b.Runs {
			live[b.Slug+"/"+r.Run] = true
		}
	}
	idx := map[string]int{}
	for i, b := range v.Repos {
		idx[b.Slug] = i
	}
	touched := map[string]bool{}
	for _, f := range fin {
		slug := budget.Key(f.Slug)
		if live[slug+"/"+clean(f.Run)] {
			continue
		}
		i, ok := idx[slug]
		if !ok {
			v.Repos = append(v.Repos, RepoBlock{Slug: slug, Name: repoDisplayName(slug, cfg)})
			i = len(v.Repos) - 1
			idx[slug] = i
		}
		v.Repos[i].Finished = append(v.Repos[i].Finished, finishedRow(f, now))
		touched[slug] = true
	}
	for slug := range touched {
		i := idx[slug]
		sort.SliceStable(v.Repos[i].Finished, func(a, b int) bool {
			return v.Repos[i].Finished[a].StartedAt > v.Repos[i].Finished[b].StartedAt // newest first
		})
	}
	sort.SliceStable(v.Repos, func(i, j int) bool { return lessRepoBlock(v.Repos[i], v.Repos[j]) })
	return v
}
