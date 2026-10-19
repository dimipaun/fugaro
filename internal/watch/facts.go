package watch

import (
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// What a run's launch.json says about it: the job it executes on, the identity
// that job's name carries (plan task 3.5 step 2, design §4.1), and who started
// it.
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

// RunFacts is what a run's own launch.json says about it that its row cannot
// carry by itself: the job it is in (JobRef, plan task 3.5 step 2) and who
// started it.
//
// The two travel together because they come from the same read. The watch scan
// reads launch.json for every run it lists — live ones included — and both
// facts are stamped onto the rows a frame draws by MergeFacts. Neither is in
// the RTDB registry entry a live row is built from: nothing writes a job there,
// and a launcher-writable field would not be trustworthy if something did.
type RunFacts struct {
	JobRef
	// LaunchedBy is who started the run, as launch.json recorded it at launch:
	// a person's address, or the identity an automated launch runs as (a
	// service account, a CI runner). "" when the reader has no launch.json for
	// the run, or the one it has recorded no launcher (every run launched
	// before the launcher was recorded) — never a value this package invents.
	//
	// UNTRUSTED display text, like everything else a launcher writes: it is
	// sanitised here and shown, and nothing decides anything by it
	// (runview.Row.LaunchedBy, and the guard test beside it). It is sanitised
	// with safetext.Field rather than this package's clean because the same
	// value goes into watch --json beside fugaro runs ls --json's, which
	// sanitises it with Field (runview.Join): one launch.json must read the
	// same in both documents, cap included.
	LaunchedBy string
}

// Facts is the RunFacts of each run a scan saw, by the run's key.
type Facts map[RunKey]RunFacts

// MergeFacts stamps facts onto v's rows: the live runs of each repository block
// and the finished rows beside them. A run the map does not name is left
// exactly as it was, so a reader that could not place a run — no launch.json,
// a scan that has not run yet, a project with no published policy — degrades
// to the row it would have shown without this.
//
// It is applied to the view rather than to the queued and finished rows alone
// because a *live* run's facts are in the same place theirs are: the bucket's
// launch.json is the only record of either, and the scan reads it for every run
// it lists — live ones included.
func MergeFacts(v View, facts Facts) View {
	if len(facts) == 0 {
		return v
	}
	for i := range v.Repos {
		b := &v.Repos[i]
		for _, rows := range [][]RunRow{b.Runs, b.Finished} {
			for j := range rows {
				f, ok := facts[RunKey{Slug: b.Slug, Run: rows[j].Run}]
				if !ok {
					continue
				}
				rows[j].Job, rows[j].Profile = clean(f.Job), clean(f.Profile)
				rows[j].Provider, rows[j].Account = clean(f.Provider), clean(f.Account)
				rows[j].LaunchedBy = safetext.Field(f.LaunchedBy)
			}
		}
	}
	return v
}
