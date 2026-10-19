package runner_test

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// This file is the end-to-end half of the project-job checkout: a whole run on
// a project job, and the clone it makes for itself. It sits in a file of its
// own rather than appended to projectjob_test.go because that file is one this
// branch does not otherwise touch: integration-0.9.0 has its own changes to
// it, and a test added there would be a conflict for no reason (the
// 2026-10-19 review's merge-state finding).

// TestProjectJobClonesBlobless: a project job is a clone job, and this is the
// whole run saying so — it clones its repository itself, blobless (the shape
// every job with no derived image clones with: plan Task 17, design
// generic-tool.md §2), and goes on to install the checkout's mise tools before
// its first stage.
//
// Nothing in the run's environment says so, and nothing needs to: the project
// job's image is the installation's base image by construction
// (internal/infra's ProjectJob.Image), which bakes no checkout, and
// projectJobEnv carries none of the per-repository variables. With the
// predicate reading FUGARO_CHECKOUT alone, this run was a baked job: it cloned
// the repository whole through OpenOrClone and provisioned nothing, so a
// migrated repository's agent ran without the runtimes its mise.toml declares
// (the 2026-10-18 review's high finding).
//
// Mutation (run, restored): return r.jobEnv(CheckoutEnv) ==
// config.CheckoutClone from cloneJob() — the checkout is a full clone, whose
// config carries no partial-clone filter, and this fails.
func TestProjectJobClonesBlobless(t *testing.T) {
	b := projectRun(t, projectJobOf(), "off", "", projectPolicyFor(`["github/acme/*"]`, ""))
	projectHost(t, b)
	projectSucceeded(t, b)
	if v := testutil.Git(t, b.deps.WorkDir, "config", "remote.origin.partialclonefilter"); v != "blob:none" {
		t.Fatalf("the project job's checkout filter = %q, want blob:none: a project job's image bakes no checkout, so the run clones it the way every clone job does", v)
	}
}
