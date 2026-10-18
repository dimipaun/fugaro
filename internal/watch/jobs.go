package watch

import "github.com/dimipaun/fugaro/internal/budget"

// The job a run executes on, and the identity that job's name carries (plan
// task 3.5 step 2, design §4.1).
//
// Under the project-centric model the job a run is in is no longer one
// repository's: it is the shared job of a (profile, provider, account), whose
// name (gcp.ProjectJobName) does not spell any of the three back out. A reader
// gets them by *computing* the name of every (profile, provider, account) the
// project's published policy defines and matching (R2-5) — never by parsing
// the name — and that computation is internal/cli's, which holds the policy
// read; this package only carries the answer to the renderers.

// RunKey identifies one row: the wire slug key of its repository (as the
// view's blocks hold it) and the run id.
type RunKey struct {
	Slug, Run string
}

// RunKeyOf is the key a row of (slug, run) is looked up under, normalised
// exactly as the rows are (the wire slug key, and the run id cleaned like
// every other string that reaches the view), so a reader that reads the
// bucket never has to reproduce either rule.
func RunKeyOf(slug, run string) RunKey {
	return RunKey{Slug: budget.Key(slug), Run: clean(run)}
}

// JobRef is what is known about the Cloud Run job a run is in: the job's own
// name, and the (profile, provider, account) it was computed from.
//
// Job alone is the common case for a run the reader could place but whose name
// is not a project job's — a per-repository job (design §4.4: every baked
// workflow, and every repository of a project that has not migrated), or a
// project job of a policy that has since changed. The three are then empty,
// which is what the JSON's absent fields say.
type JobRef struct {
	Job      string
	Profile  string
	Provider string
	Account  string
}

// Jobs is the job of each run a scan saw, by the run's key.
type Jobs map[RunKey]JobRef

// MergeJobs stamps jobs onto v's rows: the live runs of each repository block
// and the finished rows beside them. A run the map does not name is left
// exactly as it was, so a reader that could not place a run — no launch.json,
// a scan that has not run yet, a project with no published policy — degrades
// to the row it would have shown without this.
//
// It is applied to the view rather than to the queued and finished rows alone
// because a *live* run's job is in the same place theirs is: the RTDB registry
// entry a live row is built from carries no job (nothing writes one there, and
// it would be launcher-writable if it did), so the bucket's launch.json is the
// only record of it, and the scan reads it for every run it lists — live ones
// included.
func MergeJobs(v View, jobs Jobs) View {
	if len(jobs) == 0 {
		return v
	}
	for i := range v.Repos {
		b := &v.Repos[i]
		for _, rows := range [][]RunRow{b.Runs, b.Finished} {
			for j := range rows {
				ref, ok := jobs[RunKey{Slug: b.Slug, Run: rows[j].Run}]
				if !ok {
					continue
				}
				rows[j].Job, rows[j].Profile = clean(ref.Job), clean(ref.Profile)
				rows[j].Provider, rows[j].Account = clean(ref.Provider), clean(ref.Account)
			}
		}
	}
	return v
}
