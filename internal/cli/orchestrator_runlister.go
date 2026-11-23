package cli

// The daemon's read of the runs in flight (R4, D2702): what `fugaro
// orchestrator run` hands `Adopt` at start-up, so that a run a session launched
// by hand is an item the tick folds and counts rather than work the project
// cannot see.
//
// Nothing about adoption is decided here — what an item looks like, where it
// lands, what a second call does are R4's (internal/orchestrator/adopt.go).
// What this file answers is the one question only something that can read the
// runs bucket has: which runs of the project's repositories are in flight right
// now, and what each one's own records say about it.
//
// A run is in flight when its launch record says an execution was started,
// nothing has written a final record for it, and the platform does not report
// that execution as ended. The three together are what the daemon's own run
// view reads a run by (orchestratorRunView), and the last one is why they are
// three: a container the platform OOM-killed writes no final record, so the
// record alone would call a run in flight that no longer exists, and adopting
// one would put a `running` item on the node for a run that never reports.
//
// The window is the longest a run may legitimately be active (backend.MaxRunTime
// plus the job's task timeout, the same bound the platform's own read of an
// active execution is made within): a run minted before it cannot be in flight,
// and the listing is what the runs bucket's own cost is spent on.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// runInFlightWindow is how far back the listing looks for a run: the longest a
// run may legitimately be active (backend.MaxRunTime, the ceiling on a run's
// own total time) plus the slack the job's task timeout adds to it
// (backend.TaskTimeoutSlack), which is the same bound the daemon's view of one
// run is read within and the platform's own bound on an execution that is still
// active.
//
// A run ID is minted before the run starts (the launch's own stamp is the
// record's), so the window is measured from the mint and can only be too wide:
// a run listed a minute early and dropped is one object read for nothing, which
// is the direction this bound fails in.
const runInFlightWindow = backend.MaxRunTime + backend.TaskTimeoutSlack

// orchestratorRunLister is the daemon's read of the runs in flight
// (RunLister): the runs bucket and the project's own repositories, and the
// platform's word on the execution behind a run that has not reported.
//
// It holds no state between calls and it is called once, at start-up: what a
// run's records say is read at that moment and never cached, which is what
// makes the second call of a daemon that restarted read the same answer as the
// first.
type orchestratorRunLister struct {
	lc   *localcfg.Config
	env  *cloudEnv
	warn io.Writer
	// now is the machine's clock, read for the listing's window and for nothing
	// else: no stamp is written from it (D1230 keeps every stamp the server's),
	// and a clock minutes out of step lists a few runs more or fewer than it
	// needed. A nil one is time.Now.
	now func() time.Time
}

var _ orchestrator.RunLister = (*orchestratorRunLister)(nil)

// RunsInFlight implements RunLister: every run of the project's repositories
// that is in flight, in the order the repositories were read.
//
// The repositories are the local config's own (`lc.Repos`), which is the
// project's set and not the bucket's: the runs bucket is the installation's, so
// a listing of its slugs would hand adoption the runs of repositories this
// project does not drive. A repository whose slug cannot be resolved — one this
// installation records no provider for — is one line and is left out, which is
// the same answer the daemon's own run view gives it (stateSlugs): nothing here
// guesses a host, because the guess would be whose bucket objects these are.
//
// An error is a read that did not happen and is handed back whole: adoption
// writes nothing at all for it, because a lister that saw half the runs is a
// daemon that cannot tell a run it adopted from one it never saw (Adopt's own
// rule).
func (l *orchestratorRunLister) RunsInFlight(ctx context.Context) ([]orchestrator.RunInFlight, error) {
	now := time.Now
	if l.now != nil {
		now = l.now
	}
	repos := make([]string, 0, len(l.lc.Repos))
	for repo := range l.lc.Repos {
		repos = append(repos, repo)
	}
	slices.Sort(repos)
	since := now().UTC().Add(-runInFlightWindow)
	var out []orchestrator.RunInFlight
	for _, repo := range repos {
		slug, err := l.env.repoSlug(repo, checkoutOf(withoutCheckout(ctx), repo))
		if err != nil {
			l.warnf("the runs of %s are not read for adoption: %s", repo, err)
			continue
		}
		ids, err := runstore.ListRunIDs(ctx, l.env.bucket.Bucket, slug, since)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			run, ok := l.runInFlight(ctx, repo, slug, id)
			if ok {
				out = append(out, run)
			}
		}
	}
	return out, nil
}

// runInFlight reads one run's own records and answers what it is, or false for
// a run that is not in flight.
//
// The reads are the bucket's and never a second listing: a run the bucket holds
// no task for is not a run this installation started (and is not one an item
// could be written for), a run with no launch record never started, and a run
// whose execution the platform has ended is over. The task is read first
// because it is the one thing every other read is about, and a run whose task
// cannot be read is one line and no run: adoption could write no item from it,
// and an item written from guesses would be a run the daemon drives by the
// wrong brief.
func (l *orchestratorRunLister) runInFlight(ctx context.Context, repo, slug, runID string) (orchestrator.RunInFlight, bool) {
	s := runstore.Open(l.env.bucket.Bucket, slug, runID)
	spec, err := s.ReadTask(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return orchestrator.RunInFlight{}, false
	case err != nil:
		l.warnf("the run %s/%s is not adopted: its task could not be read: %s", slug, task.ShortRun(runID), safetext.OneLine(err.Error()))
		return orchestrator.RunInFlight{}, false
	}
	launch, err := s.ReadLaunch(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		// No launch record is a run that never started: nothing wrote an
		// execution for it, so there is no run for an item to be about, and a
		// run the platform started outside this installation's launcher is not
		// one either (the record is what the daemon's own view reads).
		return orchestrator.RunInFlight{}, false
	case err != nil:
		l.warnf("the run %s/%s is not adopted: its launch record could not be read: %s", slug, task.ShortRun(runID), safetext.OneLine(err.Error()))
		return orchestrator.RunInFlight{}, false
	}
	rec, err := s.ReadRecord(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		rec = nil
	case err != nil:
		l.warnf("the run %s/%s is not adopted: its record could not be read: %s", slug, task.ShortRun(runID), safetext.OneLine(err.Error()))
		return orchestrator.RunInFlight{}, false
	}
	if done, _ := orchestratorOutcome(rec); done {
		return orchestrator.RunInFlight{}, false
	}
	// A run that ended paused is in flight too, and for the same reason the
	// daemon's own view counts it: the chain is suspended and this daemon
	// resumes it (D3185, R15), so it is work the project has running and an item
	// adoption must write. The execution below is deliberately not asked about
	// it — ending the execution is what a pause is (R12), so the platform's word
	// would drop every suspended run. A pause its launcher cancelled is the
	// exception (D3197): the chain is over and there is nothing to adopt.
	if rec != nil && rec.Status == runstore.StatusPaused {
		if pauseStands(pauseFactsFor(ctx, l.env, func(format string, a ...any) { l.warnf(format, a...) }), slug, runID) {
			return runOfRecord(spec, launch, runID), true
		}
		return orchestrator.RunInFlight{}, false
	}
	if over, known := l.executionOver(ctx, slug, runID, launch); known && over {
		return orchestrator.RunInFlight{}, false
	}
	return runOfRecord(spec, launch, runID), true
}

// executionOver reports whether the platform has ended a run's execution, and
// whether it could say at all. It is the daemon's own view's read of one
// execution (orchestratorRunView.executionOver) and it fails the same way: a
// read that failed, an execution the platform no longer holds and a launch
// record that names none all leave the run in flight, because adopting a run
// that has ended costs one item the tick folds, while dropping one that is
// going loses the work in flight this whole step exists for.
func (l *orchestratorRunLister) executionOver(ctx context.Context, slug, runID string, launch *runstore.Launch) (over, known bool) {
	if launch == nil || launch.Execution == "" {
		return false, false
	}
	x, err := l.env.checkExecution(ctx, nil, launch.Execution, slug, runID)
	if err != nil {
		l.warnf("the run %s/%s is adopted without the platform's word on its execution: %s", slug, task.ShortRun(runID), safetext.OneLine(err.Error()))
		return false, false
	}
	if x == nil {
		return false, false
	}
	return x.State.Terminal(), true
}

// runOfRecord is one run in flight as adoption writes it: the fields the run's
// own records carry, and nothing derived.
//
// The title is the record's own line by the rule every surface names a run by
// (task.Spec.TitleLine), the brief is the prompt the run was launched with, and
// the plan task comes from the task's own spec with the launch record's as the
// fallback — the launch writes it too, and a spec from before the field existed
// carries none.
//
// The recipe and the mode are what the run is doing: the resolved recipe's name
// (empty where the launcher left the choice to the runner), and the mode the
// spec carries, with a run that reviews pull requests named as a review whatever
// its spec says (task.Spec.ReviewPRs) — adoption leaves a review run where it is
// (codeChangeRuns), and it can only leave it if the lister names it.
func runOfRecord(spec *task.Spec, launch *runstore.Launch, runID string) orchestrator.RunInFlight {
	repo, err := task.CanonicalRepo(spec.Repo)
	if err != nil {
		// A repository this release cannot canonicalise is not one an item may
		// carry (Adopt's own rule: the field is required by the validator), so
		// it is left empty and the run is named in adoption's one fact rather
		// than written into an item nothing accepts.
		repo = ""
	}
	planTask := spec.PlanTask
	if planTask == "" && launch != nil {
		planTask = launch.PlanTask
	}
	mode := spec.Mode
	if mode == "" && len(spec.ReviewPRs) > 0 {
		mode = recipe.ModeReview
	}
	name := ""
	if spec.Recipe != nil {
		name = spec.Recipe.Name
	}
	return orchestrator.RunInFlight{
		RunID:     runID,
		Repo:      repo,
		Title:     spec.TitleLine(),
		Recipe:    name,
		RigorTier: string(spec.Tier),
		Brief:     spec.Task,
		PlanTask:  planTask,
		Mode:      mode,
	}
}

// warnf writes one line about a run or a repository this read left out. It is a
// line and never an error: what the read is for is the runs it did find, and
// the daemon's start-up is not the place to fail over one record a person can
// see for themselves (fugaro ls names the same run).
func (l *orchestratorRunLister) warnf(format string, args ...any) {
	if l.warn == nil {
		return
	}
	fmt.Fprintf(l.warn, "orchestrator: "+format+"\n", args...)
}
