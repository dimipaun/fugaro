package cli

// The duplicate guard: a launch of a plan task refuses while that task is
// already being worked on.
//
// Why it exists: in one evening ten pairs of pull requests were duplicates of
// one plan task (#2608/#2653, #2606/#2650, #2603/#2652, #2700/#2710,
// #2637/#2692), because a second launch of a task happened while an open pull
// request or a live run for the same task already existed. Money and review
// time were spent twice over on one task, and a status file cannot be the gate
// (docs/status.md is a snapshot, and a task's own pull request never touches
// it): the launch itself has to read the live data.
//
// So the guard reads what the CLI already reads for a launch decision — the
// rows `fugaro runs ls` builds, through the same loader and with the same
// noCache rule resolvePR uses, and the forge through the same provider seam
// `fugaro followups` uses (so a Bitbucket repository is read by its own
// adapter). It refuses on either half: a run in flight on the task, or a pull
// request a run of it left open.
//
// It is a fence and not a barrier, in three ways that are deliberate. A lookup
// that fails (no network, no provider credential, a bucket that cannot be
// read) warns once and launches: a guard that blocks on its own failure is
// worse than none, and the failure it would block on is this machine's rather
// than the task's. --allow-duplicate launches anyway, for the second attempt
// that is deliberate (the redundant attempts the parallelism skill plans). And
// a launch that names no plan task is not checked at all: with no task to
// duplicate there is nothing to find.

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// openDuplicateProvider is OpenProvider, a seam for the guard's one read on a
// code host (openFollowUpsProvider's pattern): whether the pull request a run
// of the plan task left is still open, read with the process environment's own
// credential, the way every read outside a run's own job is.
var openDuplicateProvider = OpenProvider

// duplicateOf is what the guard found: the run of the plan task that is in
// flight, the pull request one of its runs left open, or both. Either alone is
// a duplicate, and a run in flight may carry a pull request of its own (a
// follow-up the task's own chain is on).
type duplicateOf struct {
	// live is the run in flight, as its short ending; "" when none is.
	live string
	// status is that run's own word: launching, pending or running.
	status string
	// pr is the pull request found open on the task, 0 when none is: the run
	// in flight's own, or one a run that ended left open.
	pr int
	// prRun is the short ending of the run whose record carries that pull
	// request, when it is not the run in flight.
	prRun string
}

// run is the run to name as the task's: the one in flight when there is one,
// else the one whose record carries the open pull request. Both are a run of
// the chain --retry and --continues take.
func (d duplicateOf) run() string {
	if d.live != "" {
		return d.live
	}
	return d.prRun
}

// what names the duplicate, as the whole of the --allow-duplicate note and the
// opening of the refusal below: the plan task, and what holds it.
func (d duplicateOf) what(planTask, slug string) string {
	switch {
	case d.live != "" && d.pr != 0:
		return fmt.Sprintf("plan task %s is already being worked on (run %s is still %s, on pull request #%d of %s)", planTask, d.live, d.status, d.pr, slug)
	case d.live != "":
		return fmt.Sprintf("plan task %s is already being worked on (run %s is still %s)", planTask, d.live, d.status)
	default:
		return fmt.Sprintf("plan task %s is already being worked on (run %s left pull request #%d of %s open)", planTask, d.prRun, d.pr, slug)
	}
}

// refusal is the message a launch of the same task is refused with: what holds
// the task, that nothing was launched, and the ways out. --pr is named only
// where there is a pull request to name, and the chain is named by the run the
// guard found -- the same run --retry resolves, since an ending two runs share
// is refused there rather than guessed at.
func (d duplicateOf) refusal(planTask, slug string) string {
	ways := fmt.Sprintf("continue the chain with fugaro run --retry %s (or --continues %s), or launch a deliberate second attempt of the same task with --allow-duplicate", d.run(), d.run())
	if d.pr != 0 {
		ways = fmt.Sprintf("continue that pull request with fugaro run --pr %d, %s", d.pr, ways)
	} else {
		ways = fmt.Sprintf("wait for that run (fugaro runs ls --repo %s), %s", slug, ways)
	}
	return fmt.Sprintf("%s, so this launch would be its second run: nothing was launched. %s.", d.what(planTask, slug), ways)
}

// planTaskDuplicate is the guard itself, called by every launch shape that
// starts a new task (a plain launch and a best-of-N batch): it refuses
// o.planTask while the task is already being worked on, unless
// --allow-duplicate says the second attempt is deliberate, and it launches as
// today whenever it cannot find out. runID is the run this launch is about
// (spec.RunID), left out of the read so that a repeated --run-id reports its
// own launch instead of being refused as its own duplicate; it is "" for a
// batch, whose attempts are minted after this call.
//
// A --pr follow-up, a --retry and a --continues launch never reach it: they
// are the ways out of the refusal, and each continues a run that already
// exists rather than starting a second one.
func planTaskDuplicate(ctx context.Context, env *cloudEnv, o *runOptions, repo, slug, runID string, warn io.Writer) error {
	if o.planTask == "" {
		return nil
	}
	if warn == nil {
		warn = io.Discard
	}
	d, found := findPlanTaskDuplicate(ctx, env, o, repo, slug, runID, warn)
	if !found {
		return nil
	}
	if o.allowDuplicate {
		fmt.Fprintf(warn, "note: --allow-duplicate: %s; launching a second run of it\n", d.what(oneLine(o.planTask), slug))
		return nil
	}
	return userErr("%s", d.refusal(oneLine(o.planTask), slug))
}

// findPlanTaskDuplicate reads the plan task's own runs and answers what holds
// it, if anything does. A read it cannot make warns and answers nothing found:
// the launch goes ahead, and the warning is the whole of what the caller hears
// about the check that did not happen.
func findPlanTaskDuplicate(ctx context.Context, env *cloudEnv, o *runOptions, repo, slug, runID string, warn io.Writer) (duplicateOf, bool) {
	now := time.Now()
	// The window is the runs bucket's own lifecycle age, so every run it still
	// holds is seen, as --pr reads one pull request's chain; noCache, because
	// whether a task is being worked on is a launch decision and a cached
	// object is what a listing saw rather than what the bucket holds
	// (lsFilter's own note). The read is the same one fugaro runs ls --pr and
	// resolvePR make, over the one repository this launch resolved.
	rows, err := loadRows(ctx, env, lsFilter{slugs: []string{slug}, since: now.Add(-followUpLookback), warn: warn, noCache: true}, now)
	if err != nil {
		warnDuplicateLookup(warn, err, o.planTask)
		return duplicateOf{}, false
	}
	var (
		found  duplicateOf
		newest time.Time
		prs    []duplicatePR
	)
	for _, r := range rows {
		// The run this launch is about is not a duplicate of itself.
		if r.PlanTask != o.planTask || r.RunID == runID {
			continue
		}
		if liveStatus(r.Status) {
			// The newest one is named, so a task whose runs piled up is
			// answered with the run a reader would look at.
			if found.live == "" || r.Created.After(newest) {
				found.live, found.status, found.pr, newest = task.ShortRun(oneLine(r.Run)), r.Status, r.PR, r.Created
			}
			continue
		}
		// A run that ended may have left a pull request open, which is the
		// other half of the guard and the one the forge has to answer. A
		// review of the task's pull request carries it while its record names
		// none (runview's PRState note), so a row with no number is passed
		// over rather than read as no pull request.
		if r.PR != 0 && !slices.ContainsFunc(prs, func(p duplicatePR) bool { return p.pr == r.PR }) {
			prs = append(prs, duplicatePR{pr: r.PR, run: task.ShortRun(oneLine(r.Run)), created: r.Created})
		}
	}
	if found.live != "" {
		// A run in flight is a duplicate on its own, and the pull request it
		// carries is the row's own: the forge is asked only when no run is
		// going, which is the case this guard exists for -- the run that
		// finished and left its pull request waiting.
		return found, true
	}
	if len(prs) == 0 {
		return duplicateOf{}, false
	}
	slices.SortFunc(prs, func(a, b duplicatePR) int { return b.created.Compare(a.created) })
	p, ok, err := openPlanTaskPR(ctx, env, repo, prs, warn)
	if err != nil {
		warnDuplicateLookup(warn, err, o.planTask)
		return duplicateOf{}, false
	}
	if !ok {
		return duplicateOf{}, false
	}
	found.pr, found.prRun = p.pr, p.run
	return found, true
}

// duplicatePR is one pull request a run of the plan task carries, with the run
// whose row carries it.
type duplicatePR struct {
	pr      int
	run     string
	created time.Time
}

// openPlanTaskPR answers the first of prs the forge still has open, newest
// first. Every failure -- no repository kind, no credential, an unreachable
// host -- is the caller's to warn about and launch through: the guard refuses
// on what it read, never on what it could not read.
func openPlanTaskPR(ctx context.Context, env *cloudEnv, repo string, prs []duplicatePR, warn io.Writer) (duplicatePR, bool, error) {
	co := checkoutConfig(ctx, repo)
	if co == nil {
		co = &config.Config{}
	}
	kind := env.repoProvider(repo, co)
	if kind == "" {
		return duplicatePR{}, false, fmt.Errorf("cannot tell which git provider %s is on: set provider in its repos entry of the local config, or run this from its checkout", repo)
	}
	_, open, err := openDuplicateProvider(os.Environ(), func(w string) {
		fmt.Fprintf(warn, "fugaro: warning: %s\n", w)
	})
	if err != nil {
		return duplicatePR{}, false, err
	}
	p, _, err := open(ctx, kind, repo)
	if err != nil {
		return duplicatePR{}, false, fmt.Errorf("opening %s: %w", repo, err)
	}
	for _, c := range prs {
		info, err := p.PullRequest(ctx, c.pr)
		if err != nil {
			return duplicatePR{}, false, fmt.Errorf("reading pull request #%d of %s: %w", c.pr, repo, err)
		}
		if info.State == gitprov.PROpen {
			return c, true, nil
		}
	}
	return duplicatePR{}, false, nil
}

// liveStatus reports whether a row's status is a run in flight: the three
// words a listing uses for a run that is starting or going. They are the set
// resolvePR refuses a follow-up over, which is the CLI's existing answer to
// whether a task is being worked on, and a run whose objects cannot be read
// (runview.StatusError) is deliberately not one of them: the guard refuses on
// what it read.
func liveStatus(s string) bool {
	switch s {
	case runview.StatusLaunching, runview.StatusPending, string(runstore.StatusRunning):
		return true
	}
	return false
}

// warnDuplicateLookup is the one line a launch prints when the check could not
// run: the launch goes ahead, and the warning says so plainly rather than
// leaving the user to think the task was checked.
func warnDuplicateLookup(warn io.Writer, err error, planTask string) {
	fmt.Fprintf(warn, "fugaro: warning: could not check whether plan task %s is already being worked on: %s; launching without that check, so the task may already have a run or an open pull request\n",
		oneLine(planTask), oneLine(strings.TrimSpace(err.Error())))
}
