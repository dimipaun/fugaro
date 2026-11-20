package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// update_prindex.go is D2965's step: the pull-request index of an installation
// that predates it.
//
// The index (runstore.PRIndexDir, one object per run) is written by a run as it
// opens or follows a pull request, so an installation that upgrades holds
// entries only for the runs that START after the upgrade, and `runs ls --pr N`
// scans the whole window for every pull request a repository has ever had a run
// on. That cannot be an operator step: the HARD RULE makes `fugaro update` the
// whole upgrade, so the walk is a step of this command, once per repository,
// over the runs of the last 90 days — the window every other pull-request
// reader already bounds itself by (followup.go's followUpLookback, which is
// runs ls --pr's own default since, runs.go's prLookback).
//
// What it writes is a hint and nothing else (D2963): an entry names the pull
// request a run worked on, one object per run, and it is created only when the
// run has none — so the step never rewrites an object a run holds, waits for no
// run (a run in flight is left alone: it has no record to read yet, or a record
// that names no pull request, which is every review still going), and a second
// update writes nothing at all.
//
// Nothing here may fail the update, for the reason update_budget_mode.go's step
// may not: an entry this run could not write is an entry the next `fugaro
// update` writes, `runs ls --pr N` scans the window until then, and a step of
// the upgrade is never stopped over a derived hint.
func (e *initEngine) backfillPRIndex(ctx context.Context) {
	r, o := e.r, e.r.o
	// The door first, as in writeAdminList: this is the upgrade's step, and the
	// hook the doors share is the end of the converge (init_stages.go's
	// outcome). `fugaro setup` creates an installation, and the runs of one it
	// has just created are the runs of a release that writes its own entries.
	if o.doorName() != updateDoor.name || o.planOnly {
		return
	}
	// Then the session, the rule the steps beside it keep (update.go's
	// writeAdminList, update_marker_version.go's convergeMarkerVersion): what
	// this step writes is a cloud object, and a coding agent's session under a
	// marker `--yes`'s help promises it "changes nothing outside this machine,
	// --yes included". The gate's exception is the one D1016 names — where the
	// marks say the project is managed, the gate's own read stands in for every
	// confirmation the run would have asked, so the session IS the run that
	// converges the installation.
	if agentMarker(os.Getenv, r.stdinTerminal()) != "" && !r.gateUnderway() {
		return
	}
	// No left_for_you guard, unlike writeAdminList: that object is authorization
	// data the intake decides a caller by, while this one is a hint a reader
	// checks against the run's own records — a run that left a step for the
	// person has still converged every repository this step walks, and writing
	// the entry now rather than on the next update is what makes the run after
	// an upgrade — the run a person launches to see the new fast `--pr` — the
	// one that finds it.
	if e.lc == nil {
		return
	}
	if fakeEndpointsOnGS(e.lc, e.lc.BucketURL()) {
		// The config's endpoints are fakes (tests, emulators): the gs:// bucket
		// is not the one they stand for, and opening it would reach the real
		// network with the developer's credentials.
		return
	}
	b, err := sharedBucketOpener(ctx, e.lc.BucketURL())
	if err != nil {
		r.warn(fmt.Sprintf("the pull-request index was not backfilled: the runs bucket %s could not be opened (%s). `runs ls --pr N` scans the window until an update writes it", safetext.Field(e.lc.BucketURL()), safetext.FieldErr(err)))
		return
	}
	defer b.Close()
	now := time.Now().UTC()
	written, err := backfillPRIndexIn(ctx, b.Bucket, now, func(format string, args ...any) {
		r.warn("the pull-request index: " + fmt.Sprintf(format, args...))
	})
	if err != nil {
		r.warn(fmt.Sprintf("the pull-request index was not backfilled: %s. `runs ls --pr N` scans the window until an update writes it", safetext.FieldErr(err)))
		return
	}
	if written == 0 {
		return
	}
	fmt.Fprintf(r.w, "pull-request index: %d run(s) of the last %s now name their pull request, one object per run under runs/<repo>/%s/, so `runs ls --pr N` finds them without scanning the window. Nothing a run holds was rewritten and no run was waited for; a second `fugaro update` writes nothing.\n",
		written, prLookback, runstore.PRIndexDir)
}

// backfillPRIndexIn is the walk itself: every repository of the bucket, its
// runs of the window, and the entry of each run whose own records name a pull
// request. It answers how many entries it created, and the one failure that
// leaves the whole walk undone (the bucket could not be listed at all); a
// repository, a run or an entry the walk could not reach is a warning through
// warnf and the walk goes on.
func backfillPRIndexIn(ctx context.Context, b *blob.Bucket, now time.Time, warnf func(string, ...any)) (int, error) {
	slugs, err := runstore.ListSlugs(ctx, b)
	if err != nil {
		return 0, err
	}
	since := now.Add(-followUpLookback)
	written := 0
	for _, slug := range slugs {
		// What the repository's index already names, from one listing of
		// runs/<slug>/pr-index/ — and never a read of the entries, because what
		// the walk has to know is whether a run has an object at all: creating
		// only what is missing is what makes a second update write nothing, and
		// it is what keeps that second update off every run's records.
		indexed, err := indexedRuns(ctx, b, slug)
		if err != nil {
			warnf("the runs of %s were skipped: its index could not be listed (%v)", safetext.Field(slug), safetext.FieldErr(err))
			continue
		}
		ids, err := runstore.ListRunIDs(ctx, b, slug, since)
		if err != nil {
			warnf("the runs of %s were skipped: they could not be listed (%v)", safetext.Field(slug), safetext.FieldErr(err))
			continue
		}
		for _, id := range ids {
			if indexed[id] {
				continue
			}
			pr, kind, err := prIndexOfRun(ctx, b, slug, id)
			if err != nil {
				// A run the update found half-created — its record written and
				// its task not yet, or the other way round — is not a failure:
				// it names no pull request this walk can place, and a run in
				// flight is never waited for.
				if errors.Is(err, runstore.ErrNotFound) {
					continue
				}
				warnf("%s was not indexed: %v", safetext.Field(slug+"/"+id), safetext.FieldErr(err))
				continue
			}
			if pr == 0 {
				continue
			}
			if err := runstore.PutPRIndex(ctx, b, runstore.PRIndexEntry{RunID: id, Slug: slug, PR: pr, Kind: kind, At: now}); err != nil {
				warnf("%s was not indexed: %v", safetext.Field(slug+"/"+id), safetext.FieldErr(err))
				continue
			}
			written++
		}
	}
	return written, nil
}

// indexedRuns is the set of run IDs a repository's index already names. The
// leaf of every object under runs/<slug>/pr-index/<N>/ is a run ID (RL2's
// layout), so one flat listing of the subtree answers it — and an object a
// reader would skip is still an object the walk must not write over
// (create-if-absent), which is why nothing here decodes one.
func indexedRuns(ctx context.Context, b *blob.Bucket, slug string) (map[string]bool, error) {
	prefix := "runs/" + slug + "/" + runstore.PRIndexDir + "/"
	out := map[string]bool{}
	it := b.List(&blob.ListOptions{Prefix: prefix})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, err)
		}
		if obj.IsDir {
			continue
		}
		out[strings.TrimSuffix(path.Base(obj.Key), ".json")] = true
	}
	return out, nil
}

// prIndexOfRun is the pull request one run worked on and how it came to be on
// it, read from the run's own records — the same two objects the row reader
// reads (task.json and result.json), and the same question it asks of them
// (runs.go's onPR). A run that names no pull request, and a run whose records
// are not there at all, answers 0 and so gets no entry. The kind is how the run
// came to be on the pull request: a review of one is `reviewed`, and a run that
// opened or followed it is one of the other two.
func prIndexOfRun(ctx context.Context, b *blob.Bucket, slug, id string) (int, runstore.PRIndexKind, error) {
	s := runstore.Open(b, slug, id)
	spec, err := s.ReadTask(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("its task could not be read: %w", err)
	}
	if spec.PR > 0 {
		return spec.PR, runstore.PRIndexFollowed, nil
	}
	rec, err := s.ReadRecord(ctx)
	if err != nil {
		// A run that has written no record yet has opened nothing: a run in
		// flight is not waited for, and the entry it will need is written by the
		// run itself when it opens the pull request (the same release's runner
		// half). A record that is there but unreadable is a warning: it is the
		// object that says which pull request the run opened.
		if errors.Is(err, runstore.ErrNotFound) {
			return 0, "", nil
		}
		return 0, "", fmt.Errorf("its record could not be read: %w", err)
	}
	if rec.PR == nil || rec.PR.Number <= 0 {
		return 0, "", nil
	}
	// A review run opened no pull request and followed none: the number its
	// record names is the one its artifact stage reviewed, and it is the same
	// number `runs ls --pr N` prints its row from (runs.go's onPR). A review
	// that is still going has no number in its record yet — its task's is the
	// only one, and the review is the run RL3 reads beside the index rather
	// than through it — so it is left alone here and writes its own entry when
	// the artifact stage reaches its number (writeReviewArtifact).
	if len(spec.ReviewPRs) > 0 {
		return rec.PR.Number, runstore.PRIndexReviewed, nil
	}
	return rec.PR.Number, runstore.PRIndexOpened, nil
}
