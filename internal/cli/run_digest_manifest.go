package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file writes the manifest a report run carries (task-governance plan
// T3.6): the run records of this repository that its digest step may read.
//
// A report run reads the records it is handed and never lists — a listing is
// a bucket-level call the job's grant cannot hold (docs/design/bucket-iam.md,
// V7) — so what a job may read is decided here, on the machine that holds
// the person's credentials and already lists runs for fugaro runs ls.

// digestManifestWindow is how far back the launcher lists this repository's
// runs: the month the reports cover plus the seven days the digest step
// catches up, which is what one report run needs to have read. A test pins
// the value by seeding a run just outside it (TestDigestManifestBounded), so
// it is a constant: there is no seam to keep open for one.
const digestManifestWindow = 40 * 24 * time.Hour

// digestManifestMax bounds the manifest: the newest this many runs.
// task.MaxDigestRuns is the same bound the decoder applies to the field, so
// the launcher can never write a task its own decoder refuses. A repository
// busier than this has the cut said out loud (digestManifest), since the
// records left out are records this month's report will not carry.
var digestManifestMax = task.MaxDigestRuns

// digestManifest returns the run IDs a report run launched now may read: this
// repository's runs of the last digestManifestWindow, newest first and at
// most digestManifestMax of them, or nil for a launch that is not a report
// run. It refuses the launch while one of this repository's report runs is
// still going (refuseReportRunInFlight), and says on warn when the cap cut
// the list. It is the one place that decides which records a report run
// reads, and so a security change (G2, T3.6):
//
//   - this repository only: the listing is runs/<this repository's slug>/,
//     so no manifest can name another repository's run. On a project job the
//     bucket does not isolate repositories (G2, T3.2), so there the runner's
//     own slug check is the guard and this is only the second one.
//   - bounded: newest first, at most digestManifestMax, with the cut warned
//     about rather than made in silence. The decoder checks each ID's shape
//     (task.Validate) and schemas/task.schema.json checks it again.
//   - one report run at a time: a schedule that fires twice does not pay for
//     two runs writing the same reports.
//
// It is written for a recipe whose first step is the digest step (T3.1's
// recipe.UsesDigest); every other launch stores no manifest, so the field
// stays absent from its task the way an older release wrote it.
//
// rcp is the recipe the launch resolved — nil for the catalog default — and
// root the checkout it resolved that recipe from, "" when there is none. A
// repository recipe carries its name alone, so its file is read from root the
// way refuseFollowUpRepoRecipe reads it; a recipe the launcher cannot read
// there is not a report run, because the runner reads it at the task's own
// base and refuses a bad one itself, and a manifest under a recipe that is
// not a report recipe is read by nothing (the digest step is its only
// reader).
func digestManifest(ctx context.Context, env *cloudEnv, slug, root string, rcp *task.Recipe, now time.Time, warn io.Writer) ([]string, error) {
	if !digestRecipe(root, rcp) {
		return nil, nil
	}
	ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, now.Add(-digestManifestWindow))
	if err != nil {
		return nil, remote(err)
	}
	if len(ids) > digestManifestMax {
		// Said out loud, because the cut is silent otherwise and it changes
		// what the report carries: the run writes the reports of the records
		// it is handed, so a repository busier than the cap has the oldest of
		// them left out of this month's report rather than of a later one.
		fmt.Fprintf(warn, "note: %s has %d runs in the last %d days, more than a report run reads at once; its report covers the newest %d\n",
			slug, len(ids), int(digestManifestWindow.Hours()/24), digestManifestMax)
		ids = ids[:digestManifestMax]
	}
	if err := refuseReportRunInFlight(ctx, env, slug, ids, now); err != nil {
		return nil, err
	}
	return ids, nil
}

// digestRecipe reports whether the launch is a report run: a recipe whose
// first step is the digest step (task.RecipeUsesDigest, over T3.1's
// recipe.UsesDigest). A repository recipe's task carries no text to read, so
// its file is read from root; with no root, or a file that is not there or
// does not parse, the answer is no, and the launch is an ordinary one.
func digestRecipe(root string, rcp *task.Recipe) bool {
	switch {
	case rcp == nil:
		return false
	case rcp.Source == string(recipe.SourceRepo):
		if root == "" {
			return false
		}
		data, found, err := recipe.ReadRepoFile(root, rcp.Name)
		if err != nil || !found {
			return false
		}
		return task.RecipeUsesDigest(string(data))
	}
	return task.RecipeUsesDigest(rcp.YAML)
}

// refuseReportRunInFlight refuses the launch when one of ids — this
// repository's runs, newest first — is a report run that has not finished.
// A report run is one whose task carries a manifest (the launcher writes
// digest_runs only for one, and the decoder refuses it under any other
// recipe), and it has not finished when it has no record, or a record still
// running: the reading fugaro runs shows, taken from the bucket alone, with
// no execution lookup. A run cancelled before it launched will not write
// anything, so it does not hold the slot.
//
// Only the runs inside the active horizon are read: nothing minted before it
// can still be going (checkMaxParallelFor's bound, which is why the horizon
// is shared), and it keeps this to about a day of records. Those are read
// through the worker pool fugaro runs ls uses, since a busy repository's day
// is hundreds of runs at three objects each and the launch should not wait
// for them one at a time. An unreadable object leaves a run unrecognised or,
// when only its record is corrupt, counted as going — fail closed, and for
// no longer than the horizon.
func refuseReportRunInFlight(ctx context.Context, env *cloudEnv, slug string, ids []string, now time.Time) error {
	horizon, err := activeHorizon(ctx, env)
	if err != nil {
		return err
	}
	oldest := now.Add(-horizon)
	var window []string
	for _, id := range ids {
		if at, err := runstore.RunTime(id); err == nil && at.Before(oldest) {
			break // ids are newest first
		}
		window = append(window, id)
	}
	objs := make([]runObjects, len(window))
	errs := make([]error, len(window))
	parallel(len(window), func(i int) {
		objs[i], errs[i] = readRun(ctx, env, slug, window[i])
	})
	if err := errors.Join(errs...); err != nil {
		return remote(err)
	}
	for i, ro := range objs {
		in := ro.in
		if in.Task == nil || len(in.Task.DigestRuns) == 0 {
			continue
		}
		if in.Record != nil && in.Record.Status != runstore.StatusRunning {
			continue
		}
		if in.Record == nil && in.Launch == nil && in.CancelMarker {
			continue
		}
		return userErr("run %s/%s is a report run that has not finished; %s has one report run at a time, because both would write the same reports: wait for it, or stop it with fugaro cancel %s",
			slug, window[i], slug, window[i])
	}
	return nil
}
