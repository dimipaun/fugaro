package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// update_marker_version.go is D1543's migration half, and the HARD RULE's: the
// creation fact version 3 recorded a bound on the billable image builds a
// managed apply may start, this release writes no such count (a build leases
// its estimated cost from the budget the runs spend from, D1554), and removing
// a field from a format is a version bump — so `fugaro update`, the one command
// the upgrade is, rewrites a version-3 fact in place as the version this build
// writes. No operator step, and nothing to delete and write again.
//
// Why the step is here and not in a command's hands: the fact is the authority
// for whether an installation's confirmations may be skipped, so growing or
// shrinking it is the converge's business, and the owner is never asked to
// touch the object — `gcloud storage cat` and a hand-written replacement is
// exactly the manual repair the update rule forbids. What the rewrite carries
// over is the amount the project chose, unchanged and unchecked by anything
// here (managed.ConvergeMarker is the writer and the judge of what may be
// converged).
//
// The door is the update's and nothing else: `fugaro setup` shows a plan and
// creates an installation, and an installation that exists converges when it is
// updated. It applies nothing under --plan-only either, which is a preview of a
// plan and not a writer of facts.
//
// Nothing here may fail the update, for the reason update_budget_mode.go's step
// may not: every state that leaves the fact alone is a warning or silence, and
// the version-3 fact keeps working — this build reads it as a version-3 fact
// and holds the count in it to nothing, so an installation whose update could
// not reach its runs bucket is exactly as managed as it was before the run.
func (e *initEngine) convergeMarkerVersion(ctx context.Context) {
	r, o := e.r, e.r.o
	if o.doorName() != updateDoor.name || o.planOnly {
		return
	}
	// A run that never opened the cloud has no bucket to read and no object to
	// rewrite, and it has already said why it stopped: a second failure over a
	// step the owner never has to run is what the update rule forbids.
	if e.c == nil {
		return
	}
	// The version this run's own gate read answered with, before a read of this
	// step's own: a fact that is not version 3 is one this step has nothing to
	// do about, and asking the bucket again to be told so is a read the
	// installation pays for on every update it ever runs. It is also what keeps
	// the step's cost off an installation that has no fact at all — the
	// unmanaged project whose runs bucket does not exist, where a read of this
	// step's own finds a refusal and would have to say something about it,
	// while the run already knows there is nothing there. The gate's answer is
	// the object's own version (read.go), so a version-3 fact with its label
	// missing reaches this step too.
	if v := r.gate.factVersion(); v != managed.MarkerVersionBuilds {
		return
	}
	// The bucket the managed read looks in, by its default name: a fact kept
	// anywhere else is one no reader of this build ever finds (readFactObject
	// derives the name from the project), so converging it there would be a
	// write nobody reads. What names the installation's own bucket is the
	// outputs when the run read them, and the selected config otherwise.
	bucket, named := e.runsBucketFor()
	if named != "" && named != bucket {
		r.warn(fmt.Sprintf("the creation fact was left as it is: this run was given a project whose runs bucket reads as %s, and the managed read only looks for the fact in %s, so a fact converged elsewhere would be one nothing reads. Check runs_bucket in the project config", safetext.Field(named), bucket))
		return
	}
	number, err := infra.ProjectNumber(ctx, e.c, r.gcpProject)
	if err != nil {
		r.warn(fmt.Sprintf("the creation fact was left as it is: %s's project number could not be read (%s), and the number is what proves a runs bucket is this project's own", safetext.Field(r.gcpProject), safetext.FieldErr(err)))
		return
	}
	wrote, err := managed.ConvergeMarker(ctx, e.c.Storage, bucket, r.gcpProject, number)
	if err != nil {
		r.warn(fmt.Sprintf("the creation fact of %s records the image build count this release does not bound a build by (D1543), and it was left as it is: %s. The installation keeps working — a version-3 fact is a fact this build reads, and the count in it bounds nothing — and the next `fugaro update` converges it", safetext.Field(r.gcpProject), safetext.FieldErr(err)))
		return
	}
	if !wrote {
		return
	}
	fmt.Fprintf(r.w, "creation fact: gs://%s/%s converged to version %d, which records the budget amount and no image build count: nothing bounds how many image builds run, and each one leases its estimated cost from the budget the runs spend from. The amount this project chose is what the fact records, unchanged.\n",
		bucket, managed.MarkerObject, managed.MarkerVersionNoBuilds)
}

// runsBucketFor is the runs bucket the two managed-fact steps read and write,
// under the default name the managed read looks in, with what the installation
// itself names when it names something ("" when nothing does).
//
// It is one function rather than the same switch in both because the two steps
// ask it for the same reason — whether the fact they would write is the one the
// read reads — and a bucket a project keeps somewhere else is one whose fact no
// reader of this build finds, whichever step is writing.
func (e *initEngine) runsBucketFor() (bucket, named string) {
	bucket = "fugaro-runs-" + e.r.gcpProject
	switch {
	case e.r.res.Outputs != nil:
		named = e.r.res.Outputs.RunsBucket
	case e.lc != nil:
		named = e.lc.RunsBucketName()
	}
	return bucket, named
}
