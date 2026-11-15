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
// So the guard reads the rows `fugaro runs ls` builds — the same loader, with
// the same noCache rule resolvePR uses — and nothing else: a run in flight on
// the task, or a run of it that carries a pull request, is a duplicate. It
// reads no code host, which is the point of this shape rather than a
// limitation worked around. The launcher holds no App key and no Bitbucket
// token (docs/git-providers.md): opening the forge from a launch would fail on
// an ordinary operator's machine — every launch warns and goes ahead, and the
// guard is worth nothing exactly where the duplicates happened — and, where
// the operator does export one, it would mint an installation token from a
// coding agent's session, which is the read every other path refuses there.
// A run's own record already says what it left: the run id, and the pull
// request with the state the listing folds the records into (markPRStates).
//
// The cost is that the records cannot see a merge: a run that left its pull
// request ready reads ready after a person merged it, so re-running a task
// whose pull request is done is refused too. That is the safe direction for a
// guard whose whole purpose is a launch that would spend the money twice, it
// is a refusal with a way out rather than a silent stop, and the refusal says
// so.
//
// It is a fence and not a barrier, in three ways that are deliberate. A read
// that fails (a bucket that cannot be read, a backend that cannot be listed)
// warns once and launches: a guard that blocks on its own failure is worse
// than none, and the failure it would block on is this machine's rather than
// the task's. --allow-duplicate launches anyway, for the second attempt that
// is deliberate (the redundant attempts the parallelism skill plans). And a
// launch that names no plan task is not checked at all: with no task to
// duplicate there is nothing to find.

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// duplicateOf is what the guard found: the run of the plan task that is in
// flight, the pull request a run of it left, or both. Either alone is a
// duplicate, and a run in flight may carry a pull request of its own (a
// follow-up the task's own chain is on).
type duplicateOf struct {
	// live is the run in flight, as the <repo-slug>/<run-id> ref the refusal's
	// commands take; "" when none is. The prose shortens it to its ending
	// (task.ShortRun) and the commands may not: a bare ending is a ref neither
	// --retry nor --continues accepts.
	live string
	// status is that run's own word: launching, pending or running.
	status string
	// pr is the pull request found on the task, 0 when none is: the run in
	// flight's own, or one a run that ended left behind.
	pr int
	// prRun is the same ref for the run whose row carries that pull request,
	// when it is not the run in flight.
	prRun string
	// prState is that pull request as the records fold it (markPRStates):
	// runview's live, ready or stalled, the words `fugaro runs ls --state`
	// matches. "" when no pull request was found.
	prState string
}

// run is the run to name as the task's: the one in flight when there is one,
// else the one whose row carries the pull request. The refusal names the chain
// --continues takes with it exactly when the guard found no pull request: where
// it found one, that pull request is what the chain is continued by.
func (d duplicateOf) run() string {
	if d.live != "" {
		return d.live
	}
	return d.prRun
}

// prWord is the pull request's state as a reader of the refusal meets it: the
// listing's own word where it is a fact about the pull request after its runs
// ended, and the sentence for live, where the row of the task is not itself
// the run that is going.
func (d duplicateOf) prWord() string {
	switch d.prState {
	case prStateReady, prStateStalled:
		return d.prState
	case prStateLive:
		return "still being worked on"
	}
	return "still there"
}

// what names the duplicate, as the whole of the --allow-duplicate note and the
// opening of the refusal below: the plan task, and what holds it. A run is
// named by its ending, the way every listing shows it; the repository is the
// caller's own <owner/name> and never the runs bucket's slug, which no command
// the refusal prints would accept.
func (d duplicateOf) what(planTask, repo string) string {
	switch {
	case d.live != "" && d.pr != 0:
		return fmt.Sprintf("plan task %s is already being worked on (run %s is still %s, on pull request #%d of %s)", planTask, task.ShortRun(d.live), d.status, d.pr, repo)
	case d.live != "":
		return fmt.Sprintf("plan task %s is already being worked on (run %s is still %s)", planTask, task.ShortRun(d.live), d.status)
	default:
		return fmt.Sprintf("plan task %s is already being worked on (run %s left pull request #%d of %s, %s)", planTask, task.ShortRun(d.prRun), d.pr, repo, d.prWord())
	}
}

// refusal is the message a launch of the same task is refused with: what holds
// the task, that nothing was launched, and the ways out -- each of them a
// command the CLI takes and that starts a run, because a way out that is itself
// refused, or that launches nothing, sends the caller into a second error
// instead of out of the first.
//
// So two flags are deliberately missing from it. --retry starts only a run that
// never launched (its own help), and every run this guard can name has: the one
// in flight holds a launch claim or a launch.json, and the one behind a pull
// request ended. existingLaunch answers a --retry of either with
// already-launched, so the launch does nothing. --continues is named only where
// the guard found no pull request at all: a run that is part of a pull request's
// chain is continued by naming the pull request, and chainFrom refuses a
// --continues of one (it would inherit what is left of that chain without being
// in it), so beside --pr it would be the same dead end. Where it is named it is
// the full <slug>/<run-id> ref --continues takes, since an ending two runs share
// is refused there rather than guessed at.
func (d duplicateOf) refusal(planTask, repo string) string {
	second := "launch a deliberate second attempt of the same task with --allow-duplicate"
	if d.pr != 0 {
		// The caveat belongs where the pull request is named and nowhere else:
		// the records cannot see a merge, so one that is done with still
		// refuses, and the caller has to hear that before it reaches for the
		// flag.
		second += " (the run records, and not the code host, are where that pull request is read from, so one since merged or closed refuses too)"
	}
	var ways string
	if d.pr != 0 {
		ways = fmt.Sprintf("continue that pull request with fugaro run --pr %d, or %s", d.pr, second)
	} else {
		ways = fmt.Sprintf("continue the chain with fugaro run --continues %s, or wait for that run (fugaro runs ls --repo %s), or %s", d.run(), repo, second)
	}
	return fmt.Sprintf("%s, so this launch would be its second run: nothing was launched. %s.", d.what(planTask, repo), ways)
}

// planTaskDuplicate is the guard itself, called by every launch shape that
// starts a new task (a plain launch and a best-of-N batch): it refuses
// o.planTask while the task is already being worked on, unless
// --allow-duplicate says the second attempt is deliberate, and it launches as
// today whenever it cannot find out. slug is the repository's name in the runs
// bucket, which is all the read below is made over; repo is the caller's own
// <owner/name> for it, which is what the refusal names, since no command it
// prints takes the slug. runID is the run this launch is about (spec.RunID),
// left out of the read so that a repeated --run-id reports its own launch
// instead of being refused as its own duplicate; it is "" for a batch, whose
// attempts are minted after this call.
//
// A --pr follow-up, a --retry and a --continues launch never reach it: they
// are the ways out of the refusal, and each continues a run that already
// exists rather than starting a second one.
func planTaskDuplicate(ctx context.Context, env *cloudEnv, o *runOptions, slug, repo, runID string, warn io.Writer) error {
	if o.planTask == "" {
		return nil
	}
	if warn == nil {
		warn = io.Discard
	}
	d, found := findPlanTaskDuplicate(ctx, env, o, slug, runID, warn)
	if !found {
		return nil
	}
	if o.allowDuplicate {
		fmt.Fprintf(warn, "note: --allow-duplicate: %s; launching a second run of it\n", d.what(oneLine(o.planTask), repo))
		return nil
	}
	return userErr("%s", d.refusal(oneLine(o.planTask), repo))
}

// findPlanTaskDuplicate reads the plan task's own runs and answers what holds
// it, if anything does. A read it cannot make warns and answers nothing found:
// the launch goes ahead, and the warning is the whole of what the caller hears
// about the check that did not happen.
func findPlanTaskDuplicate(ctx context.Context, env *cloudEnv, o *runOptions, slug, runID string, warn io.Writer) (duplicateOf, bool) {
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
	// The pull request's state comes from the rows themselves, exactly as a
	// listing computes it: the records' own word, and no code host read. The
	// guard is a launch decision, so it makes the call every launch-time read
	// makes and asks nothing of the network its environment may not have.
	markPRStates(rows, nil)
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
				found.live, found.status, found.pr, newest = oneLine(r.Run), r.Status, r.PR, r.Created
			}
			continue
		}
		// A run that ended left its pull request behind, and that is the other
		// half of the guard. A run whose record names no pull request left
		// none: one that failed before it pushed, or that only put the branch
		// back on its base. A review of the task's pull request carries it
		// while its record names none (runview's PRState note), so the row
		// that has the number is the one that speaks for it.
		if r.PR != 0 && !slices.ContainsFunc(prs, func(p duplicatePR) bool { return p.pr == r.PR }) {
			prs = append(prs, duplicatePR{pr: r.PR, run: oneLine(r.Run), state: r.PRState, created: r.Created})
		}
	}
	if found.live != "" {
		// A run in flight is a duplicate on its own, and the pull request it
		// carries is its row's own: the rows that ended are asked only when no
		// run is going, which is the case this guard exists for -- the run
		// that finished and left its pull request waiting.
		return found, true
	}
	if len(prs) == 0 {
		return duplicateOf{}, false
	}
	// The newest pull request is the one a reader would look at first, and the
	// one the task's newest run is on.
	slices.SortFunc(prs, func(a, b duplicatePR) int { return b.created.Compare(a.created) })
	found.pr, found.prRun, found.prState = prs[0].pr, prs[0].run, prs[0].state
	return found, true
}

// duplicatePR is one pull request a run of the plan task carries, with the run
// whose row carries it and the state the records fold it into.
type duplicatePR struct {
	pr      int
	run     string
	state   string
	created time.Time
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
	fmt.Fprintf(warn, "fugaro: warning: could not check whether plan task %s is already being worked on: %s; launching without that check, so the task may already have a run or a pull request\n",
		oneLine(planTask), oneLine(strings.TrimSpace(err.Error())))
}
