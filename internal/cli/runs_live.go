package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// liveState reports whether --state named one of the two states the live set
// answers: a run that is going, and a run whose launch is in flight. They are
// the two words a run of a repository with a long history can ask for and be
// answered from a handful of objects, because they are the only rows whose
// evidence is live — the backend's own listing of what is still running, and
// the claim a launch in flight holds. Every other word (a state a run ended
// in, and the three states of a pull request, which are a fact about every
// run of it) needs the window read run by run.
//
// `paused` is one of those others, although the chain it names is in flight
// (R15, F64). Its evidence is not live and cannot be: a pause ends the run's
// execution by design (R12) and releases its lease, so the backend holds
// nothing of it and no claim is open — what says the chain is suspended is the
// run's own record, and what says it still stands is the service's node
// (pauseStands). So --state paused is a scan, which is the read it has always
// been, and neither word here changes meaning for it.
func liveState(state string) bool {
	return state == string(runstore.StatusRunning) || state == runview.StatusLaunching
}

// liveSet is what the live query found: the runs that were live, and the
// executions it found them from.
type liveSet struct {
	refs  []lsRef
	execs map[string]backend.Execution
}

// liveRuns answers the two live states from the live set: the backend's
// non-terminal executions, which name the run each belongs to (FUGARO_RUN,
// backend.Execution.Run, the same binding checkExecution insists on), plus
// the launch claims of the claim window (F2, D2960). No run of the window is
// listed and no finished run's objects are read.
//
// The set is a superset of the runs those two states select, and deliberately
// so: the rest of loadRows reads each run it is given, joins it and drops the
// rows --state leaves out, exactly as it does for a scan. A run it read that
// turns out to have ended (a double launch, an execution that ended between
// the listing and the read) is therefore read and dropped rather than
// guessed into a row.
//
// execs travels back with the refs because it is the join's answer too: the
// one listing that found the runs is the listing that names each of them
// their execution, so the join below makes one backend call and no per-run
// execution read (U2).
//
// What it cannot see is a run whose own record still says running while the
// backend holds no execution of it: nothing but that run's object says it is
// going, and reading the objects of every run of the window is the scan these
// two states exist to avoid. A scan reads such a run as `running` (runview's
// "no execution found and the run has no deadline" cases are the ones that
// settle it otherwise) and a live query leaves it out -- the reading of a run
// the platform has lost, whose only witness is the record it wrote before it
// died.
//
// Nor can it see a claim on a run ID minted before the claim window: a
// relaunch under an older run ID -- `run --retry`, or a re-issue by the
// resident launcher, both of which launch an attempt's stored run id again --
// holds a fresh claim on a run the claim-window listing of run ids does not
// reach, so a scan reads that run as `launching` and this query leaves it out.
// The claim pass is bounded by the claim window because that is the only bound
// a claim's own staleness allows (F2): a claim says nothing about the run it is
// on, so the alternative -- the window's own run ids, one claim probe each --
// spends a probe on every run of the window, on --state running too, which is
// the cost these two states exist to avoid (D2960).
func liveRuns(ctx context.Context, env *cloudEnv, f lsFilter, now time.Time) (liveSet, error) {
	lf := backend.ListFilter{ActiveOnly: true}
	if !f.since.IsZero() {
		// The hour of slack the scan's own execution listing keeps: a run ID is
		// minted before its launch only by moments, but clocks differ, so a
		// listing bounded by the runs' own window is bounded an hour earlier.
		lf.Since = f.since.Add(-time.Hour)
	}
	list, err := env.be.List(ctx, lf)
	if err != nil {
		return liveSet{}, remote(fmt.Errorf("listing executions: %w", err))
	}
	want := make(map[string]bool, len(f.slugs))
	for _, slug := range f.slugs {
		want[slug] = true
	}
	set := liveSet{execs: make(map[string]backend.Execution, len(list))}
	seen := make(map[lsRef]bool, len(list))
	add := func(r lsRef) {
		if seen[r] {
			return
		}
		seen[r] = true
		set.refs = append(set.refs, r)
	}
	for _, e := range list {
		r, ok := executionRun(e)
		if !ok || !want[r.slug] || !inWindow(r, f.since) {
			continue
		}
		if id, ok := backend.ParseExecution(e.Name); ok {
			set.execs[id.Key()] = e
		}
		add(r)
	}
	for _, slug := range f.slugs {
		ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, now.Add(-runstore.ClaimTTL))
		if err != nil {
			return liveSet{}, remote(err)
		}
		for _, id := range ids {
			r := lsRef{slug, id}
			// A run the executions already found is a run whose claim is the
			// one that launch took and kept (a claim is released only when the
			// launch failed), so it is not read again here; a run the --since
			// window leaves out is not one this listing means to read; and a
			// run ID older than the claim window is not listed at all, which
			// is the one run the two states can miss -- a relaunch under it
			// (liveRuns' comment).
			if seen[r] || !inWindow(r, f.since) {
				continue
			}
			claim, err := absent(runstore.Open(env.bucket.Bucket, slug, id).ReadClaim(ctx))
			if err != nil {
				return liveSet{}, remote(err)
			}
			// The bound runview joins a claim by: a claim the claim window did
			// not have to reach -- one written by a clock behind this one --
			// reads here exactly as it reads there, as no claim at all.
			if claim == nil || now.Sub(claim.At) >= runstore.ClaimTTL {
				continue
			}
			add(r)
		}
	}
	return set, nil
}

// executionRun is the run an execution belongs to, from the FUGARO_RUN it
// reports. ok is false for an execution the launcher gave none (a run of an
// image older than the override, or an execution that is not a run's at all):
// no run of the bucket names it, so no listing of runs has a row for it.
func executionRun(e backend.Execution) (lsRef, bool) {
	slug, id, ok := strings.Cut(e.Run, "/")
	if !ok || slug == "" || !task.ValidRunID(id) {
		return lsRef{}, false
	}
	return lsRef{slug, id}, true
}

// inWindow reports whether a run is one a listing bounded by since means
// to read. The bound is the run ID's own mint time, the same one
// runstore.ListRunIDs windows the scan's own references by: an execution (or a
// claim) is made after its run ID, never before, so a live run of a window is
// one of the window's runs even when the window is a day old and the launch a
// minute old. A path that names runs some other way -- a pull request's index
// -- windows them by the same rule (prRefs).
func inWindow(r lsRef, since time.Time) bool {
	if since.IsZero() {
		return true
	}
	t, err := runstore.RunTime(r.id)
	return err != nil || !t.Before(since)
}
