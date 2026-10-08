package watch

import (
	"sort"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// QueuedStuckAfter is how long a queued run may go since its launch claim or
// launch.json without a registry entry showing up before watch warns it
// looks stuck. It happens to match runstore.ClaimTTL (past which ls itself
// stops calling the run pending or launching), but the two are independent:
// this one tunes a display warning, that one a launch's protection window.
const QueuedStuckAfter = 10 * time.Minute

// QueuedRun is a run known only from the runs bucket: a launch claim or a
// launch.json exists, but no registry entry has appeared under /agents yet,
// so nothing about its progress is known (design docs/design/watch-queued.md).
type QueuedRun struct {
	// Run and Slug are the run id and the repository slug, in the runs
	// bucket's own (unescaped) form; MergeQueued converts them to the wire
	// keys a View's repository blocks use.
	Run, Slug string
	// Workflow, Recipe and RequestedBy are read from task.json, when it
	// parses; each is "" when not known.
	Workflow, Recipe, RequestedBy string
	// LaunchedAt is when the run entered its claimed or launched state: the
	// claim's or launch.json's own timestamp, else the run id's mint time.
	LaunchedAt time.Time
}

// queuedRow is q as a RunRow, aged as of now.
func queuedRow(q QueuedRun, now time.Time) RunRow {
	age := max(now.Sub(q.LaunchedAt), 0)
	title := q.Workflow
	if title == "" {
		title = q.RequestedBy
	}
	return RunRow{
		Run: clean(q.Run), Slug: budget.Key(q.Slug),
		Title: dash(title), Stage: "queued", Round: "-", Verify: "-", Models: "-", Auth: "-",
		Recipe: clean(q.Recipe), Workflow: clean(q.Workflow), RequestedBy: clean(q.RequestedBy),
		Age: age, HasAge: true,
		Queued: true, Stuck: age > QueuedStuckAfter,
		StartedAt: q.LaunchedAt.UnixMilli(),
	}
}

// MergeQueued adds queued as extra rows of v's repository blocks (creating a
// block for a repository with no spend, agent or kill switch of its own
// yet), sorted in among the other runs by when each began. A run already
// shown live (its registry entry has appeared) is skipped, so a run that
// started since the queued snapshot was taken never duplicates. note, when
// not "", is a one-line reason the runs bucket could not be read; it is
// sanitised and kept on v either way.
func MergeQueued(v View, cfg Config, queued []QueuedRun, note string, now time.Time) View {
	v.QueuedNote = clean(note)
	if len(queued) == 0 {
		return v
	}
	index := map[string]int{}
	live := map[[2]string]bool{}
	for i, b := range v.Repos {
		index[b.Slug] = i
		for _, r := range b.Runs {
			live[[2]string{b.Slug, r.Run}] = true
		}
	}
	touched := map[string]bool{}
	for _, q := range queued {
		row := queuedRow(q, now)
		if live[[2]string{row.Slug, row.Run}] {
			continue // already shown as a live run: it has started since
		}
		i, ok := index[row.Slug]
		if !ok {
			v.Repos = append(v.Repos, RepoBlock{Slug: row.Slug, Name: repoDisplayName(row.Slug, cfg)})
			i = len(v.Repos) - 1
			index[row.Slug] = i
		}
		v.Repos[i].Runs = append(v.Repos[i].Runs, row)
		touched[row.Slug] = true
	}
	for slug := range touched {
		sortRuns(v.Repos[index[slug]].Runs)
	}
	sort.SliceStable(v.Repos, func(i, j int) bool { return lessRepoBlock(v.Repos[i], v.Repos[j]) })
	return v
}
